// Package sourcepurge_test exercises the source/extension purge cascade against
// an ephemeral PostgreSQL instance (testdb). Tests require Docker.
package sourcepurge_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/metrics"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sourcepurge"
)

// newService builds a sourcepurge.Service over a fresh testdb with a real series,
// metrics, and breaker service.
func newService(t *testing.T, db *ent.Client) *sourcepurge.Service {
	t.Helper()
	seriesSvc := series.NewService(db, t.TempDir(), 14)
	metricsSvc := metrics.NewService(db)
	gate := sourcegate.NewService(db, settings.Static{})
	return sourcepurge.NewService(db, seriesSvc, metricsSvc, gate)
}

// addProviderChapter seeds one feed row for a provider.
func addProviderChapter(t *testing.T, db *ent.Client, providerID uuid.UUID, key string) {
	t.Helper()
	db.ProviderChapter.Create().SetSeriesProviderID(providerID).SetChapterKey(key).SaveX(context.Background())
}

// TestPurgeSource_RemovesProvidersMetricsBreaker_KeepsFilesAndReevaluates is the
// acceptance test: a purge removes the source's SeriesProviders + feed + metric +
// breaker rows, KEEPS every downloaded Chapter row and its CBZ filename
// (never-auto-delete), DELETES a chapter left a sourceless phantom (never-
// downloaded + no CBZ + no remaining source), and leaves a still-carried
// permanently_failed chapter alone.
func TestPurgeSource_RemovesProvidersMetricsBreaker_KeepsFilesAndDeletesPhantoms(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Purge Series").SetSlug("purge-series").SaveX(ctx)

	// p1 is the physical source being purged (live: provider = numeric id).
	p1 := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("100").SetProviderName("Purge Me").SetSuwayomiID(100).SetImportance(20).SaveX(ctx)
	// p2 is a different source that must survive the purge untouched.
	p2 := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("200").SetProviderName("Keep Me").SetSuwayomiID(200).SetImportance(10).SaveX(ctx)

	// p1 offers 1, 5, 9; p2 offers only 9.
	addProviderChapter(t, db, p1.ID, "1")
	addProviderChapter(t, db, p1.ID, "5")
	addProviderChapter(t, db, p1.ID, "9")
	addProviderChapter(t, db, p2.ID, "9")

	// A DOWNLOADED chapter satisfied by p1 — its row + CBZ filename must survive.
	chDL := db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("1").SetNumber(1).
		SetState(entchapter.StateDownloaded).SetFilename("purge-me-001.cbz").
		SetSatisfiedByProviderID(p1.ID).SetSatisfiedImportance(20).SaveX(ctx)
	// A permanently_failed, no-CBZ chapter carried ONLY by p1 — a sourceless PHANTOM
	// after the purge (must be DELETED, not left as a phantom "wanted" row).
	chPhantom := db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("5").SetNumber(5).
		SetState(entchapter.StatePermanentlyFailed).SetLastError("all sources exhausted").SetErrorCategory("network").SaveX(ctx)
	// A permanently_failed chapter also carried by p2 — must stay pinned (not a phantom).
	chCarried := db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("9").SetNumber(9).
		SetState(entchapter.StatePermanentlyFailed).SaveX(ctx)

	// Advisory rows keyed by id (metric) and name (breaker).
	db.SourceMetric.Create().SetSourceID("100").SetSourceName("Purge Me").SetEwmaLatencyMs(5000).SaveX(ctx)
	db.SourceCircuitState.Create().SetSourceKey("Purge Me").SetConsecutiveFailures(5).SaveX(ctx)

	svc := newService(t, db)
	summary, err := svc.PurgeSource(ctx, "100", "Purge Me")
	if err != nil {
		t.Fatalf("PurgeSource: %v", err)
	}

	want := sourcepurge.SourceSummary{
		SourceID: "100", SourceName: "Purge Me",
		SeriesAffected: 1, ProvidersRemoved: 1, ChaptersDeleted: 1, MetricsDeleted: 1, BreakerCleared: 1,
	}
	if summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}

	assertSourceFootprintGone(t, db, p1.ID, p2.ID)
	assertChapterKept(t, db, chDL.ID)
	// The sourceless phantom is DELETED.
	if n := db.Chapter.Query().Where(entchapter.IDEQ(chPhantom.ID)).CountX(ctx); n != 0 {
		t.Errorf("sourceless phantom chapter still present (%d), want 0 (deleted)", n)
	}
	// The still-carried chapter is untouched (p2 still supplies key 9).
	assertChapterState(t, db, "still-carried", chCarried.ID, entchapter.StatePermanentlyFailed)

	// Only the phantom was deleted; the downloaded + still-carried rows remain.
	if n := db.Chapter.Query().CountX(ctx); n != 2 {
		t.Errorf("Chapter rows = %d, want 2 (only the sourceless phantom deleted; CBZ + carried rows kept)", n)
	}
}

// assertSourceFootprintGone confirms the purged source's provider (p1), its feed,
// and its advisory rows are gone while the surviving source (p2) + its feed stay.
func assertSourceFootprintGone(t *testing.T, db *ent.Client, purgedProviderID, survivingProviderID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.SeriesProvider.Get(ctx, purgedProviderID); !ent.IsNotFound(err) {
		t.Errorf("purged SeriesProvider should be deleted, got err=%v", err)
	}
	if _, err := db.SeriesProvider.Get(ctx, survivingProviderID); err != nil {
		t.Errorf("surviving SeriesProvider should remain, got err=%v", err)
	}
	if n := db.ProviderChapter.Query().CountX(ctx); n != 1 {
		t.Errorf("provider-chapter feed rows = %d, want 1 (only the surviving source's feed)", n)
	}
	if n := db.SourceMetric.Query().CountX(ctx); n != 0 {
		t.Errorf("SourceMetric rows = %d, want 0", n)
	}
	if n := db.SourceCircuitState.Query().CountX(ctx); n != 0 {
		t.Errorf("SourceCircuitState rows = %d, want 0", n)
	}
}

// assertChapterKept confirms a downloaded chapter survives with its CBZ filename
// intact (satisfied_by is cleared by RemoveProvider, but the row + file remain).
func assertChapterKept(t *testing.T, db *ent.Client, chapterID uuid.UUID) {
	t.Helper()
	got := db.Chapter.GetX(context.Background(), chapterID)
	if got.State != entchapter.StateDownloaded {
		t.Errorf("downloaded chapter state = %q, want downloaded (never demoted)", got.State)
	}
	if got.Filename != "purge-me-001.cbz" {
		t.Errorf("downloaded chapter filename = %q, want purge-me-001.cbz (CBZ reference kept)", got.Filename)
	}
}

// assertChapterState confirms one chapter is in the expected state.
func assertChapterState(t *testing.T, db *ent.Client, label string, chapterID uuid.UUID, want entchapter.State) {
	t.Helper()
	if got := db.Chapter.GetX(context.Background(), chapterID).State; got != want {
		t.Errorf("%s chapter state = %q, want %q", label, got, want)
	}
}

// TestPurgeSource_ResolvesNameFromMetric proves the breaker is still cleared when
// the caller passes no name (the extension cascade path): the name is resolved
// from the SourceMetric row's denormalized source_name.
func TestPurgeSource_ResolvesNameFromMetric(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	db.SourceMetric.Create().SetSourceID("100").SetSourceName("Resolved Name").SaveX(ctx)
	db.SourceCircuitState.Create().SetSourceKey("Resolved Name").SaveX(ctx)

	svc := newService(t, db)
	summary, err := svc.PurgeSource(ctx, "100", "") // no name supplied
	if err != nil {
		t.Fatalf("PurgeSource: %v", err)
	}
	if summary.SourceName != "Resolved Name" {
		t.Errorf("resolved name = %q, want Resolved Name", summary.SourceName)
	}
	if summary.MetricsDeleted != 1 || summary.BreakerCleared != 1 {
		t.Errorf("summary = %+v, want metrics=1 breaker=1 (breaker cleared via resolved name)", summary)
	}
}

// TestPurgeSource_NoMatchingSource is a clean no-op when the source has no
// footprint at all — a purge of an already-clean source must succeed with zero
// counts (idempotent re-purge).
func TestPurgeSource_NoMatchingSource(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := newService(t, db)

	summary, err := svc.PurgeSource(ctx, "999", "Ghost")
	if err != nil {
		t.Fatalf("PurgeSource: %v", err)
	}
	if summary != (sourcepurge.SourceSummary{SourceID: "999", SourceName: "Ghost"}) {
		t.Errorf("summary = %+v, want all-zero counts", summary)
	}
}

// TestPreviewSource_CountsWithoutMutating proves the dry run reports the same
// blast radius a purge would remove and changes nothing.
func TestPreviewSource_CountsWithoutMutating(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Preview Series").SetSlug("preview-series").SaveX(ctx)
	p := db.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("100").SetProviderName("Preview Me").SetSuwayomiID(100).SaveX(ctx)
	addProviderChapter(t, db, p.ID, "1")
	addProviderChapter(t, db, p.ID, "2")
	// A wanted, no-CBZ chapter carried only by this source → a would-be phantom.
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("1").SetState(entchapter.StateWanted).SaveX(ctx)
	db.SourceMetric.Create().SetSourceID("100").SetSourceName("Preview Me").SaveX(ctx)
	db.SourceCircuitState.Create().SetSourceKey("Preview Me").SaveX(ctx)

	svc := newService(t, db)
	preview, err := svc.PreviewSource(ctx, "100", "Preview Me")
	if err != nil {
		t.Fatalf("PreviewSource: %v", err)
	}
	want := sourcepurge.SourcePreview{
		SourceID: "100", SourceName: "Preview Me",
		SeriesAffected: 1, Providers: 1, ProviderChapters: 2, ChaptersDeleted: 1, Metrics: 1, Breaker: 1,
	}
	if preview != want {
		t.Fatalf("preview = %+v, want %+v", preview, want)
	}

	// Nothing was mutated.
	if n := db.SeriesProvider.Query().CountX(ctx); n != 1 {
		t.Errorf("providers after preview = %d, want 1 (dry run must not delete)", n)
	}
	if n := db.Chapter.Query().CountX(ctx); n != 1 {
		t.Errorf("chapters after preview = %d, want 1 (dry run must not delete the phantom)", n)
	}
	if n := db.SourceMetric.Query().CountX(ctx); n != 1 {
		t.Errorf("metric rows after preview = %d, want 1 (dry run must not delete)", n)
	}
}

// TestPurgeExtension_MapsPkgNameToSourceIDs proves PurgeExtension reads the
// durable HarvestedExtension.source_ids record and purges each source — the whole
// two-source extension's footprint is gone in one call, aggregated in the summary.
func TestPurgeExtension_MapsPkgNameToSourceIDs(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	s := db.Series.Create().SetTitle("Ext Series").SetSlug("ext-series").SaveX(ctx)
	// Two sources, both provided by the extension "com.example.multi".
	pEn := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("100").SetProviderName("Multi EN").SetSuwayomiID(100).SaveX(ctx)
	pEs := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("101").SetProviderName("Multi ES").SetSuwayomiID(101).SaveX(ctx)
	addProviderChapter(t, db, pEn.ID, "1")
	addProviderChapter(t, db, pEs.ID, "1")
	db.SourceMetric.Create().SetSourceID("100").SetSourceName("Multi EN").SaveX(ctx)
	db.SourceMetric.Create().SetSourceID("101").SetSourceName("Multi ES").SaveX(ctx)
	db.SourceCircuitState.Create().SetSourceKey("Multi EN").SaveX(ctx)

	// The durable pkgName→source-ids map (the enginetopo store).
	db.HarvestedExtension.Create().
		SetPkgName("com.example.multi").SetSourceIds([]int64{100, 101}).SaveX(ctx)

	svc := newService(t, db)
	summary, err := svc.PurgeExtension(ctx, "com.example.multi")
	if err != nil {
		t.Fatalf("PurgeExtension: %v", err)
	}
	if len(summary.Sources) != 2 {
		t.Fatalf("summary.Sources = %d, want 2 (one per source id)", len(summary.Sources))
	}
	if summary.ProvidersRemoved != 2 || summary.MetricsDeleted != 2 || summary.BreakerCleared != 1 {
		t.Fatalf("summary = %+v, want providers=2 metrics=2 breaker=1", summary)
	}

	if n := db.SeriesProvider.Query().CountX(ctx); n != 0 {
		t.Errorf("providers after extension purge = %d, want 0", n)
	}
	if n := db.SourceMetric.Query().CountX(ctx); n != 0 {
		t.Errorf("metric rows after extension purge = %d, want 0", n)
	}
}

// TestPurgeExtension_UnknownExtensionIsNoop proves an extension with no durable
// row (never harvested / already pruned) purges nothing without erroring.
func TestPurgeExtension_UnknownExtensionIsNoop(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := newService(t, db)

	summary, err := svc.PurgeExtension(ctx, "com.example.ghost")
	if err != nil {
		t.Fatalf("PurgeExtension: %v", err)
	}
	if len(summary.Sources) != 0 || summary.ProvidersRemoved != 0 {
		t.Errorf("summary = %+v, want empty (no durable row ⇒ nothing to purge)", summary)
	}
}
