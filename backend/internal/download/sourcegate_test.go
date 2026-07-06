// Package download_test — integration tests proving the source-politeness gate
// (internal/sourcegate) is correctly wired into the download dispatcher: a
// cooled-down source is EXCLUDED FROM DOWNLOAD CANDIDACY (the chapter stays
// wanted, never churned to failed), a source that fails enough consecutive
// times trips and is skipped on the NEXT chapter, and a success clears the
// breaker. Tests require Docker (via testcontainers) for an ephemeral
// PostgreSQL instance.
package download_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entsourcecircuitstate "github.com/technobecet/tsundoku/internal/ent/sourcecircuitstate"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

// gateTestSettings returns a settings.Static satisfying BOTH download.RetrySettings
// and sourcegate.Thresholds, so the SAME value can be threaded into both
// download.New and sourcegate.NewService — the gate and the dispatcher must
// agree on the failure threshold / cooldown / delay.
func gateTestSettings(failureThreshold int, cooldown time.Duration) settings.Static {
	return settings.Static{
		Retries: 10, Backoff: 0, DownloadConc: 1, // sequential, no per-chapter backoff noise
		SourcesFailureThresh: failureThreshold,
		SourcesCooldownIv:    cooldown,
		SourcesMinDelay:      0, // no politeness delay — keep the test fast
	}
}

// gateCallCountFetcher counts every Fetch call and always fails with a fixed error —
// used to prove a gated-out source's chapter is never even attempted.
type gateCallCountFetcher struct {
	calls atomic.Int64
	err   error
}

func (f *gateCallCountFetcher) Fetch(context.Context, fetcher.FetchRef) (fetcher.ChapterPages, error) {
	f.calls.Add(1)
	if f.err != nil {
		return fetcher.ChapterPages{}, f.err
	}
	return fetcher.ChapterPages{
		Pages:     []fetcher.PageImage{{Data: []byte{0xAB}, Ext: "jpg"}},
		PageCount: 1,
	}, nil
}

// gateTestSeriesSeq disambiguates the slug across repeated oneSourceSeries
// calls within one test (each call creates a NEW series — the series/slug
// unique constraint would otherwise collide when a test calls it more than
// once for the "same" provider).
var gateTestSeriesSeq atomic.Int64

// oneSourceSeries seeds a NEW series with n chapters, each offered by exactly
// one SeriesProvider (provider, importance 5) with a distinct chapter_key.
func oneSourceSeries(ctx context.Context, t *testing.T, client *ent.Client, provider string, n int) []*ent.Chapter {
	t.Helper()
	seq := gateTestSeriesSeq.Add(1)
	slug := fmt.Sprintf("gate-test-%s-%d", provider, seq)
	s := client.Series.Create().SetTitle("Gate Test " + slug).SetSlug(slug).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider(provider).SetImportance(5).SaveX(ctx)
	chapters := make([]*ent.Chapter, 0, n)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("c%d-%d", seq, i)
		client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey(key).SetURL("https://x/" + key).SetProviderIndex(i).SaveX(ctx)
		ch := client.Chapter.Create().SetSeries(s).SetChapterKey(key).SaveX(ctx)
		chapters = append(chapters, ch)
	}
	return chapters
}

// TestGate_PreTrippedSourceExcludedFromCandidacy_StaysWanted proves a source
// whose circuit-breaker is ALREADY tripped (cooldown_until in the future) is
// excluded from download candidacy entirely: RunOnce makes no fetch attempt and
// the chapter stays wanted — it must NOT be churned through downloading→failed.
func TestGate_PreTrippedSourceExcludedFromCandidacy_StaysWanted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const provider = "Comix"
	chapters := oneSourceSeries(ctx, t, client, provider, 1)

	// Pre-trip the breaker: cooldown_until is one hour in the future.
	client.SourceCircuitState.Create().
		SetSourceKey(provider).
		SetConsecutiveFailures(5).
		SetCooldownUntil(time.Now().Add(time.Hour)).
		SetLastError("simulated prior block").
		SaveX(ctx)

	rs := gateTestSettings(3, 30*time.Minute)
	gate := sourcegate.NewService(client, rs)
	f := &gateCallCountFetcher{}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := f.calls.Load(); got != 0 {
		t.Errorf("fetch calls = %d, want 0 (source is cooled down, never attempted)", got)
	}
	got := client.Chapter.GetX(ctx, chapters[0].ID)
	if got.State != entchapter.StateWanted {
		t.Errorf("chapter state = %s, want wanted (excluded from candidacy, not failed)", got.State)
	}
}

// TestGate_TripsAfterThresholdFailures_NextChapterSkipped proves the breaker
// trips once a source accumulates enough consecutive failures, and that a
// SUBSEQUENT chapter for that same source is then skipped entirely (no fetch
// attempt) on the next pass — the download-candidacy exclusion this feature
// exists for.
//
// RunOnce dispatches only a BOUNDED per-source batch each pass (2x the
// per-source concurrency — see download.batchPerSource), not a source's whole
// backlog, so the test drives RunOnce in a bounded loop until the breaker
// trips rather than assuming one pass reaches the threshold.
func TestGate_TripsAfterThresholdFailures_NextChapterSkipped(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const provider = "Comix"
	const threshold = 3

	// Seed `threshold` chapters, all offered only by the failing source.
	oneSourceSeries(ctx, t, client, provider, threshold)

	rs := gateTestSettings(threshold, time.Hour)
	gate := sourcegate.NewService(client, rs)
	f := &gateCallCountFetcher{err: errors.New("simulated Cloudflare block")}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

	const maxPasses = 10
	tripped := false
	for i := 0; i < maxPasses && !tripped; i++ {
		if _, err := d.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce (pass %d): %v", i, err)
		}
		tripped = !gate.IsAvailable(ctx, provider, time.Now())
	}
	if !tripped {
		t.Fatalf("source did not trip within %d passes (%d fetch calls made)", maxPasses, f.calls.Load())
	}
	if got := f.calls.Load(); got < threshold {
		t.Errorf("fetch calls before tripping = %d, want >= %d (threshold)", got, threshold)
	}
	callsBeforeNewChapter := f.calls.Load()

	// A brand-new chapter for the SAME (now-tripped) source must be skipped
	// entirely — no fetch attempt, and it stays wanted.
	newChapters := oneSourceSeries(ctx, t, client, provider, 1)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce (after trip): %v", err)
	}

	if got := f.calls.Load(); got != callsBeforeNewChapter {
		t.Errorf("fetch calls after the tripped pass = %d, want unchanged %d (source is tripped, must not be attempted)", got, callsBeforeNewChapter)
	}
	got := client.Chapter.GetX(ctx, newChapters[0].ID)
	if got.State != entchapter.StateWanted {
		t.Errorf("new chapter state = %s, want wanted (source tripped, excluded from candidacy)", got.State)
	}
}

// TestGate_SuccessClearsBreaker proves a successful fetch resets the breaker's
// consecutive-failure counter, so a source that recovers is not left one
// failure away from tripping forever.
func TestGate_SuccessClearsBreaker(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const provider = "Comix"
	const threshold = 3

	chapters := oneSourceSeries(ctx, t, client, provider, 1)

	// Seed the breaker with 2 prior consecutive failures (one short of the
	// threshold-3 trip) and no cooldown.
	client.SourceCircuitState.Create().
		SetSourceKey(provider).
		SetConsecutiveFailures(threshold - 1).
		SetLastError("simulated prior failure").
		SaveX(ctx)

	rs := gateTestSettings(threshold, time.Hour)
	gate := sourcegate.NewService(client, rs)
	f := &gateCallCountFetcher{} // succeeds
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got := client.Chapter.GetX(ctx, chapters[0].ID)
	if got.State != entchapter.StateDownloaded {
		t.Fatalf("chapter state = %s, want downloaded", got.State)
	}

	row, err := client.SourceCircuitState.Query().Where(entsourcecircuitstate.SourceKeyEQ(provider)).Only(ctx)
	if err != nil {
		t.Fatalf("load breaker row: %v", err)
	}
	if row.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after success = %d, want 0 (reset)", row.ConsecutiveFailures)
	}
	if row.CooldownUntil != nil {
		t.Error("CooldownUntil should be nil after a success")
	}
}

// TestGate_NilGateBehavesLikePrePolitenessDefault proves a Dispatcher built
// with a nil gate (the safe default for callers that don't need the gate)
// never filters candidacy — preserving pre-Slice-A behaviour exactly. This
// guards against a future regression where a nil gate accidentally starts
// blocking downloads.
func TestGate_NilGateBehavesLikePrePolitenessDefault(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	const provider = "Comix"
	chapters := oneSourceSeries(ctx, t, client, provider, 1)

	// Even with a breaker row that WOULD be tripped if a real gate were wired,
	// a nil gate must ignore it entirely.
	client.SourceCircuitState.Create().
		SetSourceKey(provider).
		SetConsecutiveFailures(99).
		SetCooldownUntil(time.Now().Add(time.Hour)).
		SaveX(ctx)

	rs := gateTestSettings(3, time.Hour)
	f := &gateCallCountFetcher{}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, nil)

	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := f.calls.Load(); got != 1 {
		t.Errorf("fetch calls = %d, want 1 (nil gate must never filter candidacy)", got)
	}
	got := client.Chapter.GetX(ctx, chapters[0].ID)
	if got.State != entchapter.StateDownloaded {
		t.Errorf("chapter state = %s, want downloaded (nil gate = pre-politeness behaviour)", got.State)
	}
}

// upgradeGateMaxRetries is the per-source budget the upgrade-gate tests use; the
// low source is seeded EXHAUSTED (attempts == this) so the tripped high source is
// the only live candidate — making the gate the sole thing standing between the
// upgrade path and a fetch.
const upgradeGateMaxRetries = 3

// upgradeGateProviders names the two sources in the upgrade-gate fixture.
const (
	upgradeGateLowProvider  = "DiskSource"
	upgradeGateHighProvider = "Comix"
)

// seedUpgradeGateFixture builds a downloaded chapter satisfied by an EXHAUSTED
// low source (importance 2) plus a strictly-higher high source (importance 5)
// whose circuit-breaker is TRIPPED. It returns the chapter, a dispatcher wired
// with the gate, and the shared fetch counter. Because the low source is
// exhausted, the high (gated) source is the only live candidate — so a correct
// gate yields exactly 0 fetches.
func seedUpgradeGateFixture(ctx context.Context, t *testing.T) (*ent.Client, *ent.Chapter, *download.Dispatcher, *gateCallCountFetcher) {
	t.Helper()
	client := testdb.New(t)

	s := client.Series.Create().SetTitle("Gate Upgrade").SetSlug("gate-upgrade").SaveX(ctx)
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider(upgradeGateLowProvider).SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey("c1").
		SetURL("https://low/c1").SetProviderIndex(0).SetAttempts(upgradeGateMaxRetries).SaveX(ctx)
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider(upgradeGateHighProvider).SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey("c1").
		SetURL("https://high/c1").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").
		SetState(entchapter.StateDownloaded).
		SetSatisfiedByProviderID(spLow.ID).SetSatisfiedImportance(2).
		SetFilename("[DiskSource] Gate Upgrade 001.cbz").SetPageCount(1).SetDownloadDate(time.Now()).
		SaveX(ctx)

	client.SourceCircuitState.Create().SetSourceKey(upgradeGateHighProvider).
		SetConsecutiveFailures(5).SetCooldownUntil(time.Now().Add(time.Hour)).SaveX(ctx)

	// Retries == upgradeGateMaxRetries so the upgrade path (which reads MaxRetries
	// from settings) also sees the low source as exhausted.
	rs := settings.Static{
		Retries: upgradeGateMaxRetries, Backoff: 0, DownloadConc: 1,
		SourcesFailureThresh: upgradeGateMaxRetries, SourcesCooldownIv: time.Hour, SourcesMinDelay: 0,
	}
	gate := sourcegate.NewService(client, rs)
	f := &gateCallCountFetcher{}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: mustTempDir(t)}, rs, gate)
	return client, ch, d, f
}

// setUpgradeGateHighBreaker trips (tripped=true) or clears (tripped=false) the
// high source's circuit-breaker.
func setUpgradeGateHighBreaker(ctx context.Context, t *testing.T, client *ent.Client, tripped bool) {
	t.Helper()
	row := client.SourceCircuitState.Query().Where(entsourcecircuitstate.SourceKeyEQ(upgradeGateHighProvider)).OnlyX(ctx)
	u := client.SourceCircuitState.UpdateOne(row)
	if tripped {
		u.SetConsecutiveFailures(5).SetCooldownUntil(time.Now().Add(time.Hour))
	} else {
		u.SetConsecutiveFailures(0).ClearCooldownUntil()
	}
	u.ExecX(ctx)
}

// TestGate_UpgradeDetectSkipsTrippedSource proves DetectUpgrades does NOT flag a
// chapter for upgrade to a source whose circuit-breaker is tripped (preventing an
// upgrade_available flag/revert flap while the source is blocked), and — as a
// non-vacuous check — DOES flag it once the breaker clears. Part of the MEDIUM
// fix (the upgrade path was previously ungated).
func TestGate_UpgradeDetectSkipsTrippedSource(t *testing.T) {
	ctx := context.Background()
	client, ch, d, _ := seedUpgradeGateFixture(ctx, t)

	flagged, err := d.DetectUpgrades(ctx, upgradeGateMaxRetries)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if flagged != 0 {
		t.Fatalf("flagged = %d, want 0 (the higher source is gated)", flagged)
	}
	if got := client.Chapter.GetX(ctx, ch.ID).State; got != entchapter.StateDownloaded {
		t.Fatalf("chapter state = %s, want downloaded (must not be flagged)", got)
	}

	// Non-vacuous: clearing the breaker makes the SAME source flag — proving the
	// gate (not some other exclusion) suppressed the flag above.
	setUpgradeGateHighBreaker(ctx, t, client, false)
	flagged, err = d.DetectUpgrades(ctx, upgradeGateMaxRetries)
	if err != nil {
		t.Fatalf("DetectUpgrades (breaker cleared): %v", err)
	}
	if flagged != 1 {
		t.Fatalf("flagged after clearing breaker = %d, want 1 (proves the gate excluded it)", flagged)
	}
}

// TestGate_UpgradeFetchSkipsTrippedSource_NotStranded proves the upgrade FETCH
// path (defense-in-depth) never fetches a gated source even when a chapter is
// already upgrade_available (a stale flag from before the trip): 0 fetch calls,
// and the chapter is left downloaded — NOT stranded in upgrade_available.
func TestGate_UpgradeFetchSkipsTrippedSource_NotStranded(t *testing.T) {
	ctx := context.Background()
	client, ch, d, f := seedUpgradeGateFixture(ctx, t)

	// Force a stale upgrade_available flag (the high source is already tripped).
	client.Chapter.UpdateOneID(ch.ID).SetState(entchapter.StateUpgradeAvailable).ExecX(ctx)

	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if got := f.calls.Load(); got != 0 {
		t.Errorf("fetch calls = %d, want 0 (the gated source must not be fetched by the upgrade path)", got)
	}
	final := client.Chapter.GetX(ctx, ch.ID)
	if final.State != entchapter.StateDownloaded {
		t.Errorf("chapter state = %s, want downloaded (must not strand in upgrade_available)", final.State)
	}
	if final.SatisfiedImportance == nil || *final.SatisfiedImportance != 2 {
		t.Errorf("satisfied_importance = %v, want 2 (never upgraded to the gated source)", final.SatisfiedImportance)
	}
}
