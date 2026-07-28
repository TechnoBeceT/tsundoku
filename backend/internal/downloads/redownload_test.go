package downloads_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/settings"
)

// cutoff is the re-download filter's "downloaded since" instant every fixture in
// this file is positioned around. It stands in for the real incident timestamp
// (the moment a bad setting started corrupting rendered CBZs).
var cutoff = time.Date(2026, 7, 25, 8, 39, 52, 0, time.UTC)

// redownloadSeed holds the ids the re-download assertions target.
type redownloadSeed struct {
	seriesID uuid.UUID
	// comixAll is the "Comix" source with no scanlator (all-scanlators provider).
	comixAll uuid.UUID
	// comixValir is the "Comix" source narrowed to the "Valir Scans" scanlator —
	// a DIFFERENT SeriesProvider row, because a provider is a (source, scanlator)
	// pair.
	comixValir uuid.UUID
	// otherSource is an unrelated source that must never be swept up.
	otherSource uuid.UUID

	// afterCutoff is downloaded, satisfied by comixAll, download_date AFTER the
	// cutoff — the plain match.
	afterCutoff uuid.UUID
	// rewritten is THE TRAP FIXTURE: first_downloaded_at is BEFORE the cutoff but
	// download_date is AFTER it (an upgrade rewrote the file). Selecting on
	// first_downloaded_at misses it; selecting on download_date catches it.
	rewritten uuid.UUID
	// freshRowOldFile is the INVERSE trap: first_downloaded_at AFTER the cutoff
	// but download_date BEFORE it. A first_downloaded_at selector would wrongly
	// include it.
	freshRowOldFile uuid.UUID
	// beforeCutoff is downloaded by the same source but written before the cutoff.
	beforeCutoff uuid.UUID
	// valirChapter is downloaded, satisfied by the Valir-Scans provider, after the
	// cutoff — only a scanlator-narrowed filter should isolate it.
	valirChapter uuid.UUID
	// otherChapter belongs to a different source entirely.
	otherChapter uuid.UUID
	// notDownloaded is wanted (no file), so nothing to replace.
	notDownloaded uuid.UUID
	// noDownloadDate is downloaded but carries no download_date at all.
	noDownloadDate uuid.UUID
}

// seedRedownload builds one series carrying three sources and the seven chapters
// that span the filter's whole decision surface (see redownloadSeed's fields).
// Every downloaded chapter also gets a ProviderChapter feed row on EVERY source so
// the per-source retry reset has something to clear.
func seedRedownload(ctx context.Context, t *testing.T, client *ent.Client) redownloadSeed {
	t.Helper()

	series := client.Series.Create().
		SetTitle("Comix Saga").SetSlug("comix-saga").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	// A live-ingested provider stores the numeric source id in `provider` and the
	// display name in `provider_name`; the canonical source name is the latter.
	comixAll := client.SeriesProvider.Create().
		SetSeriesID(series.ID).SetProvider("42").SetProviderName("Comix").SetLanguage("en").
		SetImportance(30).SaveX(ctx)
	comixValir := client.SeriesProvider.Create().
		SetSeriesID(series.ID).SetProvider("42").SetProviderName("Comix").SetScanlator("Valir Scans").
		SetLanguage("en").SetImportance(20).SaveX(ctx)
	// A disk-reconciled provider stores the display NAME in `provider` and leaves
	// `provider_name` empty — the canonical name resolution must cover it too.
	other := client.SeriesProvider.Create().
		SetSeriesID(series.ID).SetProvider("Hive Scans").SetLanguage("en").
		SetImportance(10).SaveX(ctx)

	mk := func(key string, state entchapter.State, satisfiedBy *uuid.UUID, downloadDate, firstDownloaded *time.Time) uuid.UUID {
		c := client.Chapter.Create().
			SetSeriesID(series.ID).SetChapterKey(key).
			SetState(state)
		if satisfiedBy != nil {
			c = c.SetSatisfiedByProviderID(*satisfiedBy)
		}
		if downloadDate != nil {
			c = c.SetDownloadDate(*downloadDate).SetFilename(fmt.Sprintf("[42][en] Comix Saga %s.cbz", key))
		}
		if firstDownloaded != nil {
			c = c.SetFirstDownloadedAt(*firstDownloaded)
		}
		row := c.SaveX(ctx)
		// Every source offers every chapter, each carrying spent retry budget so a
		// reset is observable.
		for _, sp := range []uuid.UUID{comixAll.ID, comixValir.ID, other.ID} {
			client.ProviderChapter.Create().
				SetSeriesProviderID(sp).SetChapterKey(key).
				SetAttempts(3).SetLastError("boom").SetNextAttemptAt(cutoff.Add(time.Hour)).
				SaveX(ctx)
		}
		return row.ID
	}

	after := cutoff.Add(2 * time.Hour)
	before := cutoff.Add(-48 * time.Hour)

	return redownloadSeed{
		seriesID:    series.ID,
		comixAll:    comixAll.ID,
		comixValir:  comixValir.ID,
		otherSource: other.ID,

		afterCutoff:     mk("c-after", entchapter.StateDownloaded, &comixAll.ID, &after, &after),
		rewritten:       mk("c-rewritten", entchapter.StateDownloaded, &comixAll.ID, &after, &before),
		freshRowOldFile: mk("c-fresh-row-old-file", entchapter.StateDownloaded, &comixAll.ID, &before, &after),
		beforeCutoff:    mk("c-before", entchapter.StateDownloaded, &comixAll.ID, &before, &before),
		valirChapter:    mk("c-valir", entchapter.StateDownloaded, &comixValir.ID, &after, &after),
		otherChapter:    mk("c-other", entchapter.StateDownloaded, &other.ID, &after, &after),
		notDownloaded:   mk("c-wanted", entchapter.StateWanted, nil, nil, nil),
		noDownloadDate:  mk("c-no-date", entchapter.StateDownloaded, &comixAll.ID, nil, &after),
	}
}

// comixFilter is the plain "every Comix chapter written since the cutoff" filter.
func comixFilter() downloads.RedownloadFilter {
	return downloads.RedownloadFilter{Source: "Comix", Since: cutoff}
}

// chapterByID reloads a chapter so post-mutation state can be asserted.
func chapterByID(ctx context.Context, t *testing.T, client *ent.Client, id uuid.UUID) *ent.Chapter {
	t.Helper()
	row, err := client.Chapter.Get(ctx, id)
	if err != nil {
		t.Fatalf("reload chapter %s: %v", id, err)
	}
	return row
}

// TestRedownloadChapter_DownloadedGoesBackToWanted proves the NEW state edge:
// a downloaded chapter is re-queued, and — QCAT-343's load-bearing claim — its
// filename is LEFT IN PLACE so the existing CBZ stays on disk until the fresh
// render overwrites it.
func TestRedownloadChapter_DownloadedGoesBackToWanted(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)
	svc := downloads.NewService(client)

	before := chapterByID(ctx, t, client, seed.afterCutoff)
	if before.Filename == "" {
		t.Fatal("fixture: the chapter under test must have a filename")
	}

	if err := svc.RedownloadChapter(ctx, seed.afterCutoff); err != nil {
		t.Fatalf("RedownloadChapter: %v", err)
	}

	got := chapterByID(ctx, t, client, seed.afterCutoff)
	if got.State != entchapter.StateWanted {
		t.Errorf("state = %s; want wanted", got.State)
	}
	if got.Filename != before.Filename {
		t.Errorf("filename = %q; want it PRESERVED as %q (QCAT-343: the existing CBZ stays until the new one lands)", got.Filename, before.Filename)
	}
}

// TestRedownloadChapter_ResetsEverySourceBudget proves the re-download hands every
// source offering the chapter a fresh per-source budget, exactly as an owner retry
// does — otherwise an exhausted source stays excluded and the re-download silently
// falls through to a worse source (or nowhere).
func TestRedownloadChapter_ResetsEverySourceBudget(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)

	if err := downloads.NewService(client).RedownloadChapter(ctx, seed.afterCutoff); err != nil {
		t.Fatalf("RedownloadChapter: %v", err)
	}

	rows := client.ProviderChapter.Query().Where().AllX(ctx)
	for _, pc := range rows {
		if pc.ChapterKey != "c-after" {
			continue
		}
		if pc.Attempts != 0 || pc.LastError != "" || pc.NextAttemptAt != nil {
			t.Errorf("provider chapter %s not reset: attempts=%d lastErr=%q next=%v", pc.ID, pc.Attempts, pc.LastError, pc.NextAttemptAt)
		}
	}
}

// TestRedownloadChapter_MatchesRetryResetExactly is the anti-drift proof for §2:
// the chapter-side reset a re-download applies is byte-for-byte the one an owner
// RETRY applies (both route through applyChapterRetryReset), so the two can never
// diverge. Two chapters carrying identical failure bookkeeping — one failed, one
// downloaded — must land in the same post-state on every reset field.
func TestRedownloadChapter_MatchesRetryResetExactly(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)
	svc := downloads.NewService(client)

	stale := cutoff.Add(time.Hour)
	// Dirty the re-download target with the full failure bookkeeping a retry clears.
	client.Chapter.UpdateOneID(seed.afterCutoff).
		SetRetries(7).SetLastError("scrambled").SetErrorCategory("parse").SetNextAttemptAt(stale).ExecX(ctx)
	// A failed twin carrying the identical bookkeeping, for the retry path.
	failed := client.Chapter.Create().
		SetSeriesID(seed.seriesID).SetChapterKey("c-failed-twin").
		SetState(entchapter.StateFailed).
		SetRetries(7).SetLastError("scrambled").SetErrorCategory("parse").SetNextAttemptAt(stale).
		SaveX(ctx)

	if err := svc.RedownloadChapter(ctx, seed.afterCutoff); err != nil {
		t.Fatalf("RedownloadChapter: %v", err)
	}
	if err := svc.RetryChapter(ctx, failed.ID); err != nil {
		t.Fatalf("RetryChapter: %v", err)
	}

	redownloaded := chapterByID(ctx, t, client, seed.afterCutoff)
	retried := chapterByID(ctx, t, client, failed.ID)
	if redownloaded.State != retried.State {
		t.Errorf("state diverged: redownload=%s retry=%s", redownloaded.State, retried.State)
	}
	if redownloaded.Retries != retried.Retries {
		t.Errorf("retries diverged: redownload=%d retry=%d", redownloaded.Retries, retried.Retries)
	}
	if redownloaded.LastError != retried.LastError {
		t.Errorf("last_error diverged: redownload=%q retry=%q", redownloaded.LastError, retried.LastError)
	}
	if redownloaded.ErrorCategory != retried.ErrorCategory {
		t.Errorf("error_category diverged: redownload=%q retry=%q", redownloaded.ErrorCategory, retried.ErrorCategory)
	}
	if redownloaded.NextAttemptAt != nil || retried.NextAttemptAt != nil {
		t.Errorf("next_attempt_at not cleared on both: redownload=%v retry=%v", redownloaded.NextAttemptAt, retried.NextAttemptAt)
	}
}

// TestRedownloadChapter_RefusesChapterWithNoFile proves a re-download is NOT a
// retry: a chapter with no CBZ to replace is refused with the 409 sentinel rather
// than quietly re-queued.
func TestRedownloadChapter_RefusesChapterWithNoFile(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)

	err := downloads.NewService(client).RedownloadChapter(ctx, seed.notDownloaded)
	if !errors.Is(err, downloads.ErrNotRedownloadable) {
		t.Fatalf("err = %v; want ErrNotRedownloadable", err)
	}
	if got := chapterByID(ctx, t, client, seed.notDownloaded); got.State != entchapter.StateWanted {
		t.Errorf("state = %s; the refused chapter must be untouched", got.State)
	}
}

// TestRedownloadChapter_NotFound maps an unknown id to the shared 404 sentinel.
func TestRedownloadChapter_NotFound(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()

	err := downloads.NewService(client).RedownloadChapter(ctx, uuid.New())
	if !errors.Is(err, downloads.ErrChapterNotFound) {
		t.Fatalf("err = %v; want ErrChapterNotFound", err)
	}
}

// TestRetryPath_UnchangedByRedownload pins the constraint that the NEW edge must
// not widen the ORDINARY retry: `downloaded` is still not a retryable state, and
// RetryChapter still refuses a downloaded chapter that has no failing source.
func TestRetryPath_UnchangedByRedownload(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)

	if downloads.IsRetryableState(entchapter.StateDownloaded) {
		t.Error("IsRetryableState(downloaded) = true; the retry set must stay {failed, permanently_failed}")
	}

	// Clear the seeded per-source failures so the chapter has no failing carrier —
	// the only other door RetryChapter opens for a downloaded chapter.
	client.ProviderChapter.Update().SetAttempts(0).ExecX(ctx)

	err := downloads.NewService(client).RetryChapter(ctx, seed.afterCutoff)
	if !errors.Is(err, downloads.ErrNotRetryable) {
		t.Fatalf("RetryChapter(downloaded) = %v; want ErrNotRetryable (the retry path must be unchanged)", err)
	}
}

// TestRedownloadPreview_SelectsByDownloadDate is THE trap test (QCAT-345). The
// filter MUST key on chapters.download_date, which a rewrite updates, and NEVER on
// first_downloaded_at, which records only the first arrival. On the live library
// the wrong column under-reported by 40x.
//
// The two decisive fixtures disagree on purpose:
//   - c-rewritten     — first_downloaded_at BEFORE the cutoff, download_date AFTER
//     ⇒ MUST be selected (a first_downloaded_at selector misses it).
//   - c-fresh-row-old-file — first_downloaded_at AFTER the cutoff, download_date
//     BEFORE ⇒ must NOT be selected (a first_downloaded_at selector wrongly adds it).
func TestRedownloadPreview_SelectsByDownloadDate(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)

	got, err := downloads.NewService(client).RedownloadPreview(ctx, comixFilter())
	if err != nil {
		t.Fatalf("RedownloadPreview: %v", err)
	}

	// c-after, c-rewritten and c-valir are the three Comix chapters written since
	// the cutoff. c-fresh-row-old-file, c-before, c-other, c-wanted and c-no-date
	// are all excluded. Reported with Errorf, not Fatalf, so the two decisive
	// per-fixture assertions below still run and name WHICH column is being read.
	if got.Matched != 3 {
		t.Errorf("matched = %d; want 3 (c-after, c-rewritten, c-valir)", got.Matched)
	}

	ids := redownloadedIDs(ctx, t, client, comixFilter())
	if !ids[seed.rewritten] {
		t.Error("c-rewritten was NOT selected — the filter is keyed on first_downloaded_at, not download_date (the 40x undercount)")
	}
	if ids[seed.freshRowOldFile] {
		t.Error("c-fresh-row-old-file WAS selected — the filter is keyed on first_downloaded_at, not download_date")
	}
	if ids[seed.beforeCutoff] {
		t.Error("c-before was selected despite predating the cutoff")
	}
	if ids[seed.otherChapter] {
		t.Error("c-other was selected despite belonging to another source")
	}
	if ids[seed.notDownloaded] {
		t.Error("c-wanted was selected despite having no file to replace")
	}
	if ids[seed.noDownloadDate] {
		t.Error("c-no-date was selected despite carrying no download_date at all")
	}
}

// redownloadedIDs applies the filter and returns the set of chapter ids it moved
// back to wanted — the honest read of "what did the selector pick", taken from the
// mutation itself rather than a parallel query. Chapters that were ALREADY wanted
// before the sweep (c-wanted) are subtracted, so the result is exactly the sweep's
// own selection.
func redownloadedIDs(ctx context.Context, t *testing.T, client *ent.Client, f downloads.RedownloadFilter) map[uuid.UUID]bool {
	t.Helper()
	alreadyWanted := wantedIDs(ctx, t, client)
	if _, err := downloads.NewService(client).RedownloadAll(ctx, f); err != nil {
		t.Fatalf("RedownloadAll: %v", err)
	}
	out := map[uuid.UUID]bool{}
	for id := range wantedIDs(ctx, t, client) {
		if !alreadyWanted[id] {
			out[id] = true
		}
	}
	return out
}

// wantedIDs snapshots the ids of every chapter currently in the wanted state.
func wantedIDs(ctx context.Context, t *testing.T, client *ent.Client) map[uuid.UUID]bool {
	t.Helper()
	out := map[uuid.UUID]bool{}
	for _, ch := range client.Chapter.Query().Where(entchapter.StateEQ(entchapter.StateWanted)).AllX(ctx) {
		out[ch.ID] = true
	}
	return out
}

// TestRedownloadAll_ScanlatorNarrowing proves the filter can express a single
// (source, scanlator) provider — the shape today's first real remediation needs
// (one Comix scanlator, not all of Comix).
func TestRedownloadAll_ScanlatorNarrowing(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)

	valir := "Valir Scans"
	n, err := downloads.NewService(client).RedownloadAll(ctx, downloads.RedownloadFilter{
		Source: "Comix", Scanlator: &valir, Since: cutoff,
	})
	if err != nil {
		t.Fatalf("RedownloadAll: %v", err)
	}
	if n != 1 {
		t.Fatalf("requeued = %d; want 1 (only the Valir-Scans chapter)", n)
	}
	if got := chapterByID(ctx, t, client, seed.valirChapter); got.State != entchapter.StateWanted {
		t.Errorf("valir chapter state = %s; want wanted", got.State)
	}
	if got := chapterByID(ctx, t, client, seed.afterCutoff); got.State != entchapter.StateDownloaded {
		t.Errorf("all-scanlators chapter state = %s; a scanlator-narrowed sweep must not touch it", got.State)
	}
}

// TestRedownloadAll_MatchesDiskOriginSourceName proves the canonical source name
// resolves for a DISK-reconciled provider too, which stores the display name in
// `provider` and leaves `provider_name` empty.
func TestRedownloadAll_MatchesDiskOriginSourceName(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)

	n, err := downloads.NewService(client).RedownloadAll(ctx, downloads.RedownloadFilter{
		Source: "Hive Scans", Since: cutoff,
	})
	if err != nil {
		t.Fatalf("RedownloadAll: %v", err)
	}
	if n != 1 {
		t.Fatalf("requeued = %d; want 1", n)
	}
	if got := chapterByID(ctx, t, client, seed.otherChapter); got.State != entchapter.StateWanted {
		t.Errorf("disk-origin chapter state = %s; want wanted", got.State)
	}
}

// TestRedownloadAll_KeepsFilenamesAndResetsBudgets proves the bulk path carries the
// per-chapter primitive's two guarantees: the CBZ filename is untouched (so the
// files stay on disk) and every source offering a swept chapter gets a fresh budget.
func TestRedownloadAll_KeepsFilenamesAndResetsBudgets(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seed := seedRedownload(ctx, t, client)

	before := chapterByID(ctx, t, client, seed.rewritten).Filename
	if _, err := downloads.NewService(client).RedownloadAll(ctx, comixFilter()); err != nil {
		t.Fatalf("RedownloadAll: %v", err)
	}

	if got := chapterByID(ctx, t, client, seed.rewritten); got.Filename != before {
		t.Errorf("filename = %q; want it PRESERVED as %q", got.Filename, before)
	}
	for _, pc := range client.ProviderChapter.Query().AllX(ctx) {
		swept := pc.ChapterKey == "c-after" || pc.ChapterKey == "c-rewritten" || pc.ChapterKey == "c-valir"
		if swept && pc.Attempts != 0 {
			t.Errorf("swept %s/%s kept attempts=%d", pc.ChapterKey, pc.ID, pc.Attempts)
		}
		if !swept && pc.Attempts == 0 {
			t.Errorf("untouched chapter %s had its budget reset", pc.ChapterKey)
		}
	}
}

// TestRedownloadPreview_ReportsHonestCost proves the preview states the throughput
// cost from the SAME per-cycle batch the dispatcher enforces (2x the per-source
// download concurrency), so the owner sees the real wait rather than a guess.
func TestRedownloadPreview_ReportsHonestCost(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedRedownload(ctx, t, client)

	svc := downloads.NewService(client).WithThroughput(settings.Static{DownloadConc: 5})
	got, err := svc.RedownloadPreview(ctx, comixFilter())
	if err != nil {
		t.Fatalf("RedownloadPreview: %v", err)
	}
	if got.PerCycle != 10 {
		t.Errorf("perCycle = %d; want 10 (batchPerSource(5))", got.PerCycle)
	}
	if got.EstimatedCycles != 1 {
		t.Errorf("estimatedCycles = %d; want 1 (3 chapters at 10 per cycle, rounded up)", got.EstimatedCycles)
	}
}

// TestRedownloadPreview_CostUnknownWithoutThroughput documents the nil-accessor
// default: with no throughput port attached the preview reports 0/0 rather than
// inventing a cycle estimate.
func TestRedownloadPreview_CostUnknownWithoutThroughput(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedRedownload(ctx, t, client)

	got, err := downloads.NewService(client).RedownloadPreview(ctx, comixFilter())
	if err != nil {
		t.Fatalf("RedownloadPreview: %v", err)
	}
	if got.PerCycle != 0 || got.EstimatedCycles != 0 {
		t.Errorf("perCycle/estimatedCycles = %d/%d; want 0/0 when no throughput accessor is attached", got.PerCycle, got.EstimatedCycles)
	}
}

// TestRedownloadPreview_DeletesNothing pins QCAT-343's Rule 2 consequence: neither
// the preview nor the apply removes a Chapter row or a ProviderChapter feed row.
// Re-download adds NO new deletion path.
func TestRedownloadPreview_DeletesNothing(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	seedRedownload(ctx, t, client)

	chaptersBefore := client.Chapter.Query().CountX(ctx)
	feedBefore := client.ProviderChapter.Query().CountX(ctx)

	svc := downloads.NewService(client)
	if _, err := svc.RedownloadPreview(ctx, comixFilter()); err != nil {
		t.Fatalf("RedownloadPreview: %v", err)
	}
	if _, err := svc.RedownloadAll(ctx, comixFilter()); err != nil {
		t.Fatalf("RedownloadAll: %v", err)
	}

	if got := client.Chapter.Query().CountX(ctx); got != chaptersBefore {
		t.Errorf("chapter rows = %d; want %d (re-download deletes nothing)", got, chaptersBefore)
	}
	if got := client.ProviderChapter.Query().CountX(ctx); got != feedBefore {
		t.Errorf("provider chapter rows = %d; want %d (re-download deletes nothing)", got, feedBefore)
	}
}
