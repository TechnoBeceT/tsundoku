package series_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/series"
)

// releaseDatesByKey maps a detail's chapters to their releaseDate keyed by
// chapter key, so a test can assert per-chapter dates without depending on order.
func releaseDatesByKey(d series.SeriesDetailDTO) map[string]*time.Time {
	out := make(map[string]*time.Time, len(d.Chapters))
	for _, c := range d.Chapters {
		out[c.ChapterKey] = c.ReleaseDate
	}
	return out
}

// TestReleaseDate_FallsBackToDownloadDate proves ChapterDTO.releaseDate (QCAT-297):
// a chapter whose provider feed carries a provider_upload_date shows that date;
// a chapter with NO upload date falls back to its own download_date (owner: "when
// there is no upload date, use download date").
func TestReleaseDate_FallsBackToDownloadDate(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	upload := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	download := time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC)

	s := db.Series.Create().SetTitle("Release Date Series").SetSlug("release-date-series").SaveX(ctx)
	p := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("src").SetSuwayomiID(7).SetImportance(10).SaveX(ctx)

	// Chapter 1: the feed row carries an upload date → releaseDate = upload.
	db.ProviderChapter.Create().SetSeriesProviderID(p.ID).SetChapterKey("1").SetNumber(1).
		SetProviderUploadDate(upload).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("1").SetNumber(1).
		SetState("downloaded").SetSatisfiedByProviderID(p.ID).SetSatisfiedImportance(10).
		SetDownloadDate(download).SaveX(ctx)

	// Chapter 2: no upload date on the feed → releaseDate falls back to download_date.
	db.ProviderChapter.Create().SetSeriesProviderID(p.ID).SetChapterKey("2").SetNumber(2).SaveX(ctx)
	db.Chapter.Create().SetSeriesID(s.ID).SetChapterKey("2").SetNumber(2).
		SetState("downloaded").SetSatisfiedByProviderID(p.ID).SetSatisfiedImportance(10).
		SetDownloadDate(download).SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)
	dto, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	dates := releaseDatesByKey(dto)

	if got := dates["1"]; got == nil || !got.Equal(upload) {
		t.Errorf("chapter 1 releaseDate = %v, want the upload date %v", got, upload)
	}
	if got := dates["2"]; got == nil || !got.Equal(download) {
		t.Errorf("chapter 2 releaseDate = %v, want the download-date fallback %v", got, download)
	}
}

// TestLatestChapterAt_SeriesWideMax proves latestChapterAt is SERIES-BOUND: the
// MAX across ANY provider, not per-source. Provider B carries the newest upload,
// so the series' latestChapterAt is that date — and GetSeries (in-memory over the
// loaded feeds) and ListSeries (bounded aggregates) MUST agree.
func TestLatestChapterAt_SeriesWideMax(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	older := time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)

	s := db.Series.Create().SetTitle("Series Wide Max").SetSlug("series-wide-max").
		SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	a := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("a").SetSuwayomiID(1).SetImportance(20).SaveX(ctx)
	b := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("b").SetSuwayomiID(2).SetImportance(10).SaveX(ctx)

	// Provider A (higher importance) has the OLDER newest chapter; provider B has
	// the newest one — so a per-source view would miss it; a series-bound MAX must not.
	db.ProviderChapter.Create().SetSeriesProviderID(a.ID).SetChapterKey("1").SetNumber(1).
		SetProviderUploadDate(older).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(b.ID).SetChapterKey("2").SetNumber(2).
		SetProviderUploadDate(newest).SaveX(ctx)

	svc := series.NewService(db, t.TempDir(), 14)

	detail, err := svc.GetSeries(ctx, s.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if detail.LatestChapterAt == nil {
		t.Fatalf("GetSeries LatestChapterAt = nil, want %v", newest)
	}
	if *detail.LatestChapterAt != newest.Format(time.RFC3339) {
		t.Errorf("GetSeries LatestChapterAt = %q, want %q (MAX across ALL providers)", *detail.LatestChapterAt, newest.Format(time.RFC3339))
	}

	page, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	sum := findSummaryByID(t, page, s.ID)
	if sum.LatestChapterAt == nil || *sum.LatestChapterAt != newest.Format(time.RFC3339) {
		t.Errorf("ListSeries LatestChapterAt = %v, want %q (list must agree with detail)", sum.LatestChapterAt, newest.Format(time.RFC3339))
	}
}

// TestIsStalled_OnlyWhenMonitoredAndNotCompleted proves the owner-refined stalled
// eligibility end-to-end through both the list and detail: a series whose newest
// release is well past the threshold is stalled ONLY while monitored + not
// completed; pausing or completing it clears the flag, and a freshly-released
// series is never stalled.
func TestIsStalled_OnlyWhenMonitoredAndNotCompleted(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	svc := series.NewService(db, t.TempDir(), 14) // default stalled threshold = 30 days

	stale := time.Now().UTC().AddDate(0, 0, -40) // 40 days ago
	fresh := time.Now().UTC().AddDate(0, 0, -3)  // 3 days ago

	staleID := seedDatedSeries(ctx, t, db, "Stalled One", "stalled-one", stale)
	freshID := seedDatedSeries(ctx, t, db, "Fresh One", "fresh-one", fresh)

	// Default state: monitored=true, completed=false.
	if !detailStalled(ctx, t, svc, staleID) {
		t.Errorf("stale + monitored + not completed: isStalled = false, want true")
	}
	if detailStalled(ctx, t, svc, freshID) {
		t.Errorf("fresh series: isStalled = true, want false (within threshold)")
	}
	// The list must agree with the detail for the stale series.
	page, err := svc.ListSeries(ctx, series.ListFilter{})
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if !findSummaryByID(t, page, staleID).IsStalled {
		t.Errorf("ListSeries: stale series IsStalled = false, want true")
	}

	// Pausing (monitored=false) clears the flag — nothing to wait for.
	if err := svc.SetMonitored(ctx, staleID, false); err != nil {
		t.Fatalf("SetMonitored: %v", err)
	}
	if detailStalled(ctx, t, svc, staleID) {
		t.Errorf("stale but NOT monitored: isStalled = true, want false")
	}

	// Re-monitor then complete — completing also clears the flag.
	if err := svc.SetMonitored(ctx, staleID, true); err != nil {
		t.Fatalf("SetMonitored: %v", err)
	}
	if err := svc.SetCompleted(ctx, staleID, true); err != nil {
		t.Fatalf("SetCompleted: %v", err)
	}
	if detailStalled(ctx, t, svc, staleID) {
		t.Errorf("stale but completed: isStalled = true, want false")
	}
}

// seedDatedSeries creates a monitored, not-completed series with a single
// provider whose one feed chapter carries the given upload date — the sole knob
// the stalled tests vary.
func seedDatedSeries(ctx context.Context, t *testing.T, db *ent.Client, title, slug string, upload time.Time) uuid.UUID {
	t.Helper()
	s := db.Series.Create().SetTitle(title).SetSlug(slug).SetCategoryID(catID(ctx, db, "Manga")).SaveX(ctx)
	p := db.SeriesProvider.Create().SetSeriesID(s.ID).SetProvider("src").SetSuwayomiID(3).SetImportance(10).SaveX(ctx)
	db.ProviderChapter.Create().SetSeriesProviderID(p.ID).SetChapterKey("1").SetNumber(1).
		SetProviderUploadDate(upload).SaveX(ctx)
	return s.ID
}

// detailStalled fetches a series' detail and returns its IsStalled flag.
func detailStalled(ctx context.Context, t *testing.T, svc *series.Service, id uuid.UUID) bool {
	t.Helper()
	d, err := svc.GetSeries(ctx, id)
	if err != nil {
		t.Fatalf("GetSeries(%s): %v", id, err)
	}
	return d.IsStalled
}

// findSummary returns the page row for id (failing if absent).
func findSummaryByID(t *testing.T, page []series.SeriesSummaryDTO, id uuid.UUID) series.SeriesSummaryDTO {
	t.Helper()
	for _, s := range page {
		if s.ID == id.String() {
			return s
		}
	}
	t.Fatalf("series %s not found in ListSeries page", id)
	return series.SeriesSummaryDTO{}
}
