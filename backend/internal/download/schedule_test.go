// Package download_test — deterministic tests for the ordered, bounded,
// queued→downloading per-source scheduler (schedule.go). Concurrency is made
// deterministic with a gate fetcher that blocks each fetch on a channel the test
// controls (no sleeps-as-synchronisation). Tests require Docker (testcontainers).
package download_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/sse"
)

// mutableSettings is a download.RetrySettings whose per-source concurrency can be
// changed between cycles, so a test can prove the dispatcher reads the cap at
// use-time (hot reload) rather than capturing it at construction.
type mutableSettings struct {
	mu      sync.Mutex
	conc    int
	retries int
	backoff time.Duration
}

func (m *mutableSettings) MaxRetries(context.Context) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retries
}

func (m *mutableSettings) RetryBackoff(context.Context) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.backoff
}

func (m *mutableSettings) DownloadConcurrency(context.Context) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conc
}

func (m *mutableSettings) setConc(n int) {
	m.mu.Lock()
	m.conc = n
	m.mu.Unlock()
}

// gateFetcher makes fetch concurrency deterministic. Every fetch records its
// order and signals `started` (AFTER the dispatcher has committed the chapter to
// downloading), then — for a provider that is currently gated — blocks on the
// active release channel until the test opens it. A fetch for a provider in
// failProviders returns an error instead of a page (fall-through modelling).
type gateFetcher struct {
	mu             sync.Mutex
	started        chan string
	release        chan struct{}   // nil = gate open (fetches don't block)
	blockProviders map[string]bool // nil = block every provider; else only these
	failProviders  map[string]bool
	order          []string
}

// newGateFetcher builds a gate fetcher whose gate starts CLOSED (blocking).
func newGateFetcher() *gateFetcher {
	return &gateFetcher{
		started: make(chan string, 256),
		release: make(chan struct{}),
	}
}

// Fetch records the call, signals it started, then blocks if the provider is
// gated and the gate is closed.
func (g *gateFetcher) Fetch(_ context.Context, ref fetcher.FetchRef) (fetcher.ChapterPages, error) {
	g.mu.Lock()
	g.order = append(g.order, ref.URL)
	rel := g.release
	block := (g.blockProviders == nil || g.blockProviders[ref.Provider]) && rel != nil
	fail := g.failProviders[ref.Provider]
	g.mu.Unlock()

	g.started <- ref.URL
	if block {
		<-rel
	}
	if fail {
		return fetcher.ChapterPages{}, errors.New("provider " + ref.Provider + " is down")
	}
	return fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0xCD}, Ext: "jpg"}},
		PageCount: 1,
	}, nil
}

// open permanently releases every currently- and future-blocked fetch (the gate
// stays open until reset).
func (g *gateFetcher) open() {
	g.mu.Lock()
	if g.release != nil {
		close(g.release)
		g.release = nil
	}
	g.mu.Unlock()
}

// reset re-closes the gate so the next cycle's fetches block again.
func (g *gateFetcher) reset() {
	g.mu.Lock()
	g.release = make(chan struct{})
	g.mu.Unlock()
}

// drainStarted empties any buffered start signals left from a previous cycle so a
// subsequent waitStarted only counts the new cycle's fetches.
func (g *gateFetcher) drainStarted() {
	for {
		select {
		case <-g.started:
		default:
			return
		}
	}
}

// waitStarted blocks until n fetches have begun (deterministic barrier).
func (g *gateFetcher) waitStarted(t *testing.T, n int) {
	t.Helper()
	for range n {
		select {
		case <-g.started:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for %d fetches to start", n)
		}
	}
}

// orderSnapshot returns a copy of the recorded fetch order.
func (g *gateFetcher) orderSnapshot() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.order))
	copy(out, g.order)
	return out
}

// seedSourceChapters creates a series with n wanted chapters from a single source
// (provider), numbered 1..n with distinct keys/URLs, and returns their ids in
// ascending number order. The URL encodes the number so fetch order can be
// asserted. Each call makes its own series so keys never collide across sources.
func seedSourceChapters(ctx context.Context, t *testing.T, client *ent.Client, slug, provider string, importance, n int) []uuid.UUID {
	t.Helper()
	s := client.Series.Create().SetTitle(slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetImportance(importance).SaveX(ctx)
	ids := make([]uuid.UUID, 0, n)
	for i := range n {
		num := float64(i + 1)
		key := slug + "-" + itoa(i+1)
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).
			SetChapterKey(key).
			SetNillableNumber(&num).
			SetURL("https://" + provider + "/" + itoa(i+1)).
			SetProviderIndex(i).
			SaveX(ctx)
		ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SetNillableNumber(&num).SaveX(ctx)
		ids = append(ids, ch.ID)
	}
	return ids
}

// countStates returns how many of the given chapters are in each of the two named
// states.
func countStates(ctx context.Context, t *testing.T, client *ent.Client, ids []uuid.UUID) map[entchapter.State]int {
	t.Helper()
	counts := make(map[entchapter.State]int)
	for _, id := range ids {
		counts[client.Chapter.GetX(ctx, id).State]++
	}
	return counts
}

// itoa is a tiny non-allocating-ish int formatter (avoids strconv import churn).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestRunOnce_QueuedUntilSlotAcquired proves the queued→downloading contract and
// the per-source cap in one shot: with cap=2 and 5 chapters from one source, while
// all fetches are blocked exactly 2 chapters are in downloading and the other 3
// stay wanted (UI "Queued"). Releasing the gate drains the rest to downloaded.
func TestRunOnce_QueuedUntilSlotAcquired(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ids := seedSourceChapters(ctx, t, client, "queued", "src", 10, 5)

	g := newGateFetcher()
	d := download.New(client, g, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		&mutableSettings{conc: 2, retries: 3, backoff: time.Hour})

	done := make(chan error, 1)
	go func() { done <- d.RunOnce(ctx) }()

	// Barrier: wait until exactly the cap (2) fetches have begun. The 3rd cannot
	// start until a slot frees, so no more fetches begin while the gate is closed.
	g.waitStarted(t, 2)

	counts := countStates(ctx, t, client, ids)
	if counts[entchapter.StateDownloading] != 2 {
		t.Errorf("downloading = %d, want 2 (the cap)", counts[entchapter.StateDownloading])
	}
	if counts[entchapter.StateWanted] != 3 {
		t.Errorf("wanted = %d, want 3 (still queued behind the cap)", counts[entchapter.StateWanted])
	}

	// Open the gate: all chapters finish, RunOnce returns, everything downloaded.
	g.open()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	final := countStates(ctx, t, client, ids)
	if final[entchapter.StateDownloaded] != 5 {
		t.Errorf("downloaded = %d, want 5 after gate opened", final[entchapter.StateDownloaded])
	}
}

// TestRunOnce_StartsInNumberOrder proves that a source's chapters START in
// ascending chapter-number order. With cap=1 the queue is fully serial, so the
// recorded fetch order must be exactly 1,2,3,4,5 (URLs encode the number).
func TestRunOnce_StartsInNumberOrder(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedSourceChapters(ctx, t, client, "ordered", "src", 10, 5)

	g := newGateFetcher()
	g.open() // non-blocking: serial cap=1 execution records the true start order
	d := download.New(client, g, sse.NewHub(), download.Config{Storage: mustTempDir(t)},
		&mutableSettings{conc: 1, retries: 3, backoff: time.Hour})

	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	order := g.orderSnapshot()
	want := []string{"https://src/1", "https://src/2", "https://src/3", "https://src/4", "https://src/5"}
	if len(order) != len(want) {
		t.Fatalf("fetched %d chapters, want %d (order=%v)", len(order), len(want), order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("fetch order[%d] = %q, want %q (full order=%v)", i, order[i], want[i], order)
		}
	}
}

// TestRunOnce_DownloadConcurrencyReadAtUse proves the cap is read at use-time: the
// SAME dispatcher runs two cycles against a mutable settings whose concurrency is
// changed in between, and the number of simultaneously-downloading chapters tracks
// the new value.
func TestRunOnce_DownloadConcurrencyReadAtUse(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	ms := &mutableSettings{conc: 1, retries: 3, backoff: time.Hour}
	g := newGateFetcher()
	d := download.New(client, g, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, ms)

	// Cycle 1 at cap=1: only one chapter may be downloading while the gate is shut.
	ids1 := seedSourceChapters(ctx, t, client, "cycle1", "srcA", 10, 3)
	done1 := make(chan error, 1)
	go func() { done1 <- d.RunOnce(ctx) }()
	g.waitStarted(t, 1)
	if n := countStates(ctx, t, client, ids1)[entchapter.StateDownloading]; n != 1 {
		t.Errorf("cycle 1 (cap=1): downloading = %d, want 1", n)
	}
	g.open()
	if err := <-done1; err != nil {
		t.Fatalf("cycle 1 RunOnce: %v", err)
	}

	// Change the cap to 3 and run a second cycle on fresh chapters: now three may
	// download at once — proving the dispatcher re-read the setting.
	g.drainStarted() // discard cycle-1 start signals so the barrier below is exact
	ms.setConc(3)
	g.reset()
	ids2 := seedSourceChapters(ctx, t, client, "cycle2", "srcB", 10, 3)
	done2 := make(chan error, 1)
	go func() { done2 <- d.RunOnce(ctx) }()
	g.waitStarted(t, 3)
	if n := countStates(ctx, t, client, ids2)[entchapter.StateDownloading]; n != 3 {
		t.Errorf("cycle 2 (cap=3): downloading = %d, want 3", n)
	}
	g.open()
	if err := <-done2; err != nil {
		t.Fatalf("cycle 2 RunOnce: %v", err)
	}
}

// TestRunOnce_NoCrossSourceHeadOfLineBlocking proves a saturated source never
// stalls another source with free slots: source A's fetches are gated (blocked)
// while source B's pass through. B's chapters must reach downloaded while A's are
// still stuck downloading. Completion is observed via SSE download.done events
// (deterministic, no polling).
func TestRunOnce_NoCrossSourceHeadOfLineBlocking(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	hub := sse.NewHub()
	events, unsub := hub.Subscribe()
	defer unsub()

	aIDs := seedSourceChapters(ctx, t, client, "srcA-series", "A", 10, 2)
	bIDs := seedSourceChapters(ctx, t, client, "srcB-series", "B", 10, 2)

	g := newGateFetcher()
	g.blockProviders = map[string]bool{"A": true} // only A blocks; B passes through

	d := download.New(client, g, hub, download.Config{Storage: mustTempDir(t)},
		&mutableSettings{conc: 2, retries: 3, backoff: time.Hour})

	done := make(chan error, 1)
	go func() { done <- d.RunOnce(ctx) }()

	// Wait for source B's two chapters to complete (download.done). A cannot emit
	// download.done while its gate is shut, so these two events are B's.
	waitForDoneEvents(t, events, 2)

	// B finished while A is still stuck behind its closed gate: B was not
	// head-of-line blocked by A. A made ZERO completions (it is gated mid-fetch).
	if b := countStates(ctx, t, client, bIDs); b[entchapter.StateDownloaded] != 2 {
		t.Errorf("source B downloaded = %d, want 2 (B must not be blocked by A)", b[entchapter.StateDownloaded])
	}
	if a := countStates(ctx, t, client, aIDs); a[entchapter.StateDownloaded] != 0 {
		t.Errorf("source A downloaded = %d, want 0 (A is gated, must not have finished)", a[entchapter.StateDownloaded])
	}

	// Release A and let the cycle finish.
	g.open()
	if err := <-done; err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if a := countStates(ctx, t, client, aIDs); a[entchapter.StateDownloaded] != 2 {
		t.Errorf("source A downloaded = %d, want 2 after gate opened", a[entchapter.StateDownloaded])
	}
}

// waitForDoneEvents blocks until n download.done SSE events arrive.
func waitForDoneEvents(t *testing.T, events <-chan sse.Event, n int) {
	t.Helper()
	seen := 0
	timeout := time.After(10 * time.Second)
	for seen < n {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed after %d/%d download.done events", seen, n)
			}
			if ev.Type == "download.done" {
				seen++
			}
		case <-timeout:
			t.Fatalf("timed out after %d/%d download.done events", seen, n)
		}
	}
}
