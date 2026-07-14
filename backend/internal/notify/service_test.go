// Package notify_test drives the new-readable-chapter notifier against the
// ephemeral-Postgres harness. Each test seeds a precise chapter/series shape and
// asserts the pass fires (or suppresses) exactly, that the watermark advances
// monotonically, and — the load-bearing proof — that a convergence upgrade
// (which rewrites download_date but not first_downloaded_at) fires ZERO
// notifications.
package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/notify"
	"github.com/technobecet/tsundoku/internal/sse"
)

// fakeHub captures every broadcast so a test can assert the chapter.new event.
type fakeHub struct{ events []sse.Event }

func (h *fakeHub) Broadcast(e sse.Event) { h.events = append(h.events, e) }

// fakePusher captures every Web Push dispatch.
type fakePusher struct {
	payloads []notify.NewChapterNotification
}

func (p *fakePusher) Push(_ context.Context, payload notify.NewChapterNotification) {
	p.payloads = append(p.payloads, payload)
}

// fakeToggle is the notifications.enabled gate.
type fakeToggle struct{ on bool }

func (t fakeToggle) NotificationsEnabled(context.Context) bool { return t.on }

// catID resolves a seeded default category's id by name (testdb seeds them).
func catID(ctx context.Context, db *ent.Client, name string) uuid.UUID {
	id, err := category.IDByName(ctx, db, name)
	if err != nil {
		panic(err)
	}
	return id
}

// assertSilent fails if any broadcast or push happened.
func assertSilent(t *testing.T, hub *fakeHub, pusher *fakePusher) {
	t.Helper()
	if len(hub.events) != 0 {
		t.Fatalf("expected no broadcasts, got %d", len(hub.events))
	}
	if len(pusher.payloads) != 0 {
		t.Fatalf("expected no pushes, got %d", len(pusher.payloads))
	}
}

// assertFiredOnce asserts exactly one chapter.new broadcast + one push and
// returns the pushed payload for further assertions.
func assertFiredOnce(t *testing.T, hub *fakeHub, pusher *fakePusher) notify.NewChapterNotification {
	t.Helper()
	if len(hub.events) != 1 {
		t.Fatalf("expected 1 broadcast, got %d", len(hub.events))
	}
	if hub.events[0].Type != "chapter.new" {
		t.Fatalf("expected chapter.new, got %q", hub.events[0].Type)
	}
	if len(pusher.payloads) != 1 {
		t.Fatalf("expected 1 push, got %d", len(pusher.payloads))
	}
	return pusher.payloads[0]
}

// harness builds a notifier with capturing fakes and the given toggle state.
func harness(t *testing.T, on bool) (*ent.Client, *fakeHub, *fakePusher, *notify.Service) {
	t.Helper()
	client := testdb.New(t)
	hub := &fakeHub{}
	pusher := &fakePusher{}
	svc := notify.NewService(client, hub, pusher, fakeToggle{on: on})
	return client, hub, pusher, svc
}

// makeSeries creates a monitored, non-completed series with the given armed +
// completed + monitored state under the Manhwa category.
func makeSeries(ctx context.Context, t *testing.T, client *ent.Client, slug string, armed, monitored, completed bool) *ent.Series {
	t.Helper()
	return client.Series.Create().
		SetTitle(slug).
		SetSlug(slug).
		SetCategoryID(catID(ctx, client, "Manhwa")).
		SetNotifyArmed(armed).
		SetMonitored(monitored).
		SetCompleted(completed).
		SaveX(ctx)
}

// addChapter creates a chapter for a series with the given state; when fdl is
// non-nil it is set as first_downloaded_at (a readable chapter).
func addChapter(ctx context.Context, t *testing.T, client *ent.Client, s *ent.Series, key string, state entchapter.State, fdl *time.Time) *ent.Chapter {
	t.Helper()
	c := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey(key).
		SetState(state)
	if fdl != nil {
		c = c.SetFirstDownloadedAt(*fdl)
	}
	return c.SaveX(ctx)
}

// TestNotify_ArmedSeriesFires: an armed monitored series with a new readable
// chapter fires exactly one broadcast + one push and advances the watermark.
func TestNotify_ArmedSeriesFires(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, hub, pusher, svc := harness(t, true)

	base := time.Now().UTC().Truncate(time.Second)
	watermark := base.Add(-time.Hour)
	fdl := base

	if err := notify.SetWatermarkForTest(ctx, client, watermark); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
	s := makeSeries(ctx, t, client, "armed-series", true, true, false)
	addChapter(ctx, t, client, s, "1", entchapter.StateDownloaded, &fdl)

	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("NotifyNewChapters: %v", err)
	}

	pl := assertFiredOnce(t, hub, pusher)
	if pl.Total != 1 || len(pl.Groups) != 1 {
		t.Fatalf("unexpected payload: %+v", pl)
	}
	if pl.Groups[0].Count != 1 {
		t.Fatalf("unexpected count: %+v", pl.Groups[0])
	}
	if pl.Groups[0].URL != "/series/"+s.ID.String() {
		t.Fatalf("unexpected deep-link: %q", pl.Groups[0].URL)
	}
	assertWatermarkAfter(t, ctx, client, watermark)
}

// assertWatermarkAfter asserts the persisted watermark advanced strictly past t.
func assertWatermarkAfter(t *testing.T, ctx context.Context, client *ent.Client, prev time.Time) {
	t.Helper()
	got, present, err := notify.GetWatermarkForTest(ctx, client)
	if err != nil {
		t.Fatalf("watermark read: %v", err)
	}
	if !present {
		t.Fatalf("watermark absent")
	}
	if !got.After(prev) {
		t.Fatalf("watermark did not advance: got %v want > %v", got, prev)
	}
}

// TestNotify_FreshAdoptSuppressedThenArms: a fresh adopt's backlog is suppressed
// while chapters are still in flight; once caught up the series arms (still
// silent); the NEXT genuinely-new chapter fires.
func TestNotify_FreshAdoptSuppressedThenArms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, hub, pusher, svc := harness(t, true)

	base := time.Now().UTC().Truncate(time.Second)
	t0 := base.Add(-2 * time.Hour) // initial watermark
	t1 := base.Add(-time.Hour)     // first downloaded chapter
	t2 := base.Add(-30 * time.Minute)
	t3 := base

	if err := notify.SetWatermarkForTest(ctx, client, t0); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
	s := makeSeries(ctx, t, client, "fresh-adopt", false, true, false)
	wanted := addChapter(ctx, t, client, s, "2", entchapter.StateWanted, nil)
	addChapter(ctx, t, client, s, "1", entchapter.StateDownloaded, &t1)

	// Run 1: still in flight → suppressed, not armed.
	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("run1: %v", err)
	}
	assertSilent(t, hub, pusher)
	if client.Series.GetX(ctx, s.ID).NotifyArmed {
		t.Fatalf("run1 must not arm (still in flight)")
	}

	// Run 2: flip the wanted chapter to downloaded (caught up) → arm, still silent.
	client.Chapter.UpdateOneID(wanted.ID).SetState(entchapter.StateDownloaded).SetFirstDownloadedAt(t2).ExecX(ctx)
	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("run2: %v", err)
	}
	assertSilent(t, hub, pusher)
	if !client.Series.GetX(ctx, s.ID).NotifyArmed {
		t.Fatalf("run2 must arm the caught-up series")
	}

	// Run 3: a genuinely-new readable chapter fires.
	addChapter(ctx, t, client, s, "3", entchapter.StateDownloaded, &t3)
	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("run3: %v", err)
	}
	pl := assertFiredOnce(t, hub, pusher)
	if pl.Groups[0].Count != 1 {
		t.Fatalf("run3 count: %+v", pl)
	}
}

// TestNotify_UpgradeFiresZero (LOAD-BEARING): a convergence upgrade rewrites
// download_date to now but leaves first_downloaded_at below the watermark, so it
// must fire ZERO notifications.
func TestNotify_UpgradeFiresZero(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, hub, pusher, svc := harness(t, true)

	base := time.Now().UTC().Truncate(time.Second)
	fdl := base.Add(-2 * time.Hour)   // first became readable long ago
	watermark := base.Add(-time.Hour) // notifier already accounted past it
	downloadDate := base              // upgrade re-fetch just now

	if err := notify.SetWatermarkForTest(ctx, client, watermark); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
	s := makeSeries(ctx, t, client, "upgraded-series", true, true, false)
	client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetState(entchapter.StateDownloaded).
		SetFirstDownloadedAt(fdl).
		SetDownloadDate(downloadDate).
		SaveX(ctx)

	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("NotifyNewChapters: %v", err)
	}
	assertSilent(t, hub, pusher)
}

// TestNotify_DigestCollapse: 4 armed series each gaining a chapter collapse into
// a single digest notification.
func TestNotify_DigestCollapse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, hub, pusher, svc := harness(t, true)

	base := time.Now().UTC().Truncate(time.Second)
	watermark := base.Add(-time.Hour)
	fdl := base
	if err := notify.SetWatermarkForTest(ctx, client, watermark); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
	for i := range 4 {
		s := makeSeries(ctx, t, client, "digest-"+string(rune('a'+i)), true, true, false)
		addChapter(ctx, t, client, s, "1", entchapter.StateDownloaded, &fdl)
	}

	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("NotifyNewChapters: %v", err)
	}
	pl := assertFiredOnce(t, hub, pusher)
	if !pl.Digest {
		t.Fatalf("want digest=true, got %+v", pl)
	}
	if pl.Body != "4 new chapters across 4 series" {
		t.Fatalf("digest body: %q", pl.Body)
	}
}

// TestNotify_ToggleOff: with the toggle off nothing fires and the watermark is
// left untouched (so re-enabling does not storm the owner).
func TestNotify_ToggleOff(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, hub, pusher, svc := harness(t, false)

	base := time.Now().UTC().Truncate(time.Second)
	watermark := base.Add(-time.Hour)
	fdl := base
	if err := notify.SetWatermarkForTest(ctx, client, watermark); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
	s := makeSeries(ctx, t, client, "toggle-off", true, true, false)
	addChapter(ctx, t, client, s, "1", entchapter.StateDownloaded, &fdl)

	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("NotifyNewChapters: %v", err)
	}
	assertSilent(t, hub, pusher)
	got, present, err := notify.GetWatermarkForTest(ctx, client)
	if err != nil {
		t.Fatalf("watermark read: %v", err)
	}
	if !present {
		t.Fatalf("watermark absent")
	}
	if !got.Equal(watermark) {
		t.Fatalf("watermark changed while toggle off: got %v want %v", got, watermark)
	}
}

// TestNotify_CompletedAndUnmonitoredExcluded: completed and unmonitored series
// are outside the notify scope entirely.
func TestNotify_CompletedAndUnmonitoredExcluded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, hub, pusher, svc := harness(t, true)

	base := time.Now().UTC().Truncate(time.Second)
	watermark := base.Add(-time.Hour)
	fdl := base
	if err := notify.SetWatermarkForTest(ctx, client, watermark); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
	completed := makeSeries(ctx, t, client, "completed", true, true, true)
	addChapter(ctx, t, client, completed, "1", entchapter.StateDownloaded, &fdl)
	unmonitored := makeSeries(ctx, t, client, "unmonitored", true, false, false)
	addChapter(ctx, t, client, unmonitored, "1", entchapter.StateDownloaded, &fdl)

	if err := svc.NotifyNewChapters(ctx); err != nil {
		t.Fatalf("NotifyNewChapters: %v", err)
	}
	assertSilent(t, hub, pusher)
}
