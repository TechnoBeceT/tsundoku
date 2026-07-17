package series_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// engineSwitchFixture seeds a series with ONE source that carries the SAME physical
// chapter under two engine keys (the Suwayomi→Rensaio duplicate): a negative-numeric
// legacy chapter ("-1") and a name-keyed canonical ("name:epilogue"), both feeding
// off provider-chapter rows that share one source URL. Returns the series id and the
// two chapter ids (negative = the removable legacy twin, named = the canonical keep).
func engineSwitchFixture(t *testing.T, client *ent.Client, storage, sharedURL string,
	negRead bool, negLastPage int, negReadAt *time.Time, namedRead bool,
) (seriesID, negID, namedID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	sr := client.Series.Create().
		SetTitle("Epilogue Series").SetSlug("epilogue-series").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	sp := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("101").SetProviderName("Toonily").SetImportance(10).SaveX(ctx)

	// Two feed rows, DIFFERENT keys, SAME source URL — the identity that proves the
	// two chapters are one physical chapter re-ingested across the engine switch.
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("-1").SetURL(sharedURL).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("name:epilogue").SetURL(sharedURL).SaveX(ctx)

	negNumber := -1.0
	negC := client.Chapter.Create().
		SetSeriesID(sr.ID).SetChapterKey("-1").SetNumber(negNumber).
		SetState(entchapter.StateDownloaded).
		SetFilename("Chapter -1 [Toonily] Epilogue Series -1.cbz").
		SetRead(negRead).SetLastReadPage(negLastPage)
	if negReadAt != nil {
		negC.SetReadAt(*negReadAt)
	}
	neg := negC.SaveX(ctx)

	named := client.Chapter.Create().
		SetSeriesID(sr.ID).SetChapterKey("name:epilogue").
		SetState(entchapter.StateDownloaded).
		SetFilename("Epilogue [Toonily] Epilogue Series epilogue.cbz").
		SetRead(namedRead).SaveX(ctx)

	return sr.ID, neg.ID, named.ID
}

// TestDedupeFiles_MergesEngineSwitchDuplicateByURL proves the pass-0 merge: the
// negative-numeric legacy chapter + its feed rows + its CBZ are deleted, the
// name-keyed canonical + its feed rows survive, and the removed twin's read-state is
// transferred onto the canonical (read wins).
func TestDedupeFiles_MergesEngineSwitchDuplicateByURL(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	readAt := time.Now().UTC().Truncate(time.Second)
	seriesID, negID, namedID := engineSwitchFixture(t, client, storage,
		"/toonily/epilogue-series/epilogue", true, 7, &readAt, false)

	seriesDir := filepath.Join(storage, "Manga", "Epilogue Series")
	writeCBZ(t, seriesDir, "Chapter -1 [Toonily] Epilogue Series -1.cbz")
	writeCBZ(t, seriesDir, "Epilogue [Toonily] Epilogue Series epilogue.cbz")

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, seriesID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (one merged duplicate)", removed)
	}

	// The negative-numeric row is gone; the canonical survives.
	if exists := client.Chapter.Query().Where(entchapter.IDEQ(negID)).ExistX(ctx); exists {
		t.Error("negative-numeric chapter row still exists, want removed")
	}
	kept := client.Chapter.GetX(ctx, namedID)
	assertReadState(t, kept, true, 7, &readAt)

	// The removed key's feed rows are gone; the canonical key's feed rows survive.
	assertKeyFeedCount(t, ctx, client, "-1", 0)
	assertKeyFeedCount(t, ctx, client, "name:epilogue", 1)

	// The legacy CBZ is deleted; the canonical CBZ survives.
	assertRemainingCBZ(t, seriesDir, "Epilogue [Toonily] Epilogue Series epilogue.cbz")
}

// assertReadState fails unless the chapter's read/last_read_page/read_at match.
func assertReadState(t *testing.T, ch *ent.Chapter, wantRead bool, wantPage int, wantReadAt *time.Time) {
	t.Helper()
	if ch.Read != wantRead {
		t.Errorf("read = %v, want %v", ch.Read, wantRead)
	}
	if ch.LastReadPage != wantPage {
		t.Errorf("last_read_page = %d, want %d", ch.LastReadPage, wantPage)
	}
	switch {
	case wantReadAt == nil && ch.ReadAt != nil:
		t.Errorf("read_at = %v, want nil", ch.ReadAt)
	case wantReadAt != nil && (ch.ReadAt == nil || !ch.ReadAt.Equal(*wantReadAt)):
		t.Errorf("read_at = %v, want %v", ch.ReadAt, wantReadAt)
	}
}

// assertKeyFeedCount fails unless exactly want ProviderChapter rows carry the key.
func assertKeyFeedCount(t *testing.T, ctx context.Context, client *ent.Client, key string, want int) {
	t.Helper()
	if n := client.ProviderChapter.Query().Where(entproviderchapter.ChapterKey(key)).CountX(ctx); n != want {
		t.Errorf("provider-chapters for key %q = %d, want %d", key, n, want)
	}
}

// assertRemainingCBZ fails unless exactly the given filenames remain in dir.
func assertRemainingCBZ(t *testing.T, dir string, want ...string) {
	t.Helper()
	got := listCBZ(t, dir)
	if len(got) != len(want) {
		t.Fatalf("remaining CBZs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("remaining CBZ[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDedupeFiles_MergeKeepsAlreadyReadCanonical proves read-state transfer is a
// UNION that never downgrades: when the canonical was already read, the merge leaves
// its progress untouched even though the removed twin was unread.
func TestDedupeFiles_MergeKeepsAlreadyReadCanonical(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	seriesID, _, namedID := engineSwitchFixture(t, client, storage,
		"/toonily/epilogue-series/epilogue", false, 0, nil, true)

	seriesDir := filepath.Join(storage, "Manga", "Epilogue Series")
	writeCBZ(t, seriesDir, "Chapter -1 [Toonily] Epilogue Series -1.cbz")
	writeCBZ(t, seriesDir, "Epilogue [Toonily] Epilogue Series epilogue.cbz")

	svc := series.NewService(client, storage, 14)
	if _, err := svc.DedupeFiles(ctx, seriesID); err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}

	kept := client.Chapter.GetX(ctx, namedID)
	if !kept.Read {
		t.Error("kept chapter read flipped to false, want it to stay true")
	}
}

// TestDedupeFiles_MergeToleratesMissingCBZ proves a merged duplicate whose CBZ is
// NOT on disk (never downloaded, or a drifted on-disk name) is still merged: the row
// + feed rows are removed and no error is returned.
func TestDedupeFiles_MergeToleratesMissingCBZ(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	seriesID, negID, _ := engineSwitchFixture(t, client, storage,
		"/toonily/epilogue-series/epilogue", false, 0, nil, false)

	// Deliberately write NO CBZ files — the merge must tolerate a missing file.
	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, seriesID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (merged despite missing CBZ)", removed)
	}
	if exists := client.Chapter.Query().Where(entchapter.IDEQ(negID)).ExistX(ctx); exists {
		t.Error("negative-numeric chapter row still exists, want removed")
	}
}

// TestDedupeFiles_NoMergeWithoutSharedURL proves the CONSERVATIVE guard: without a
// shared source URL there is no proof the two rows are one chapter, so NOTHING is
// merged even though a "-1" and a "name:epilogue" chapter both exist.
func TestDedupeFiles_NoMergeWithoutSharedURL(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle("No Match Series").SetSlug("no-match-series").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("101").SetProviderName("Toonily").SetImportance(10).SaveX(ctx)

	// Different URLs — no identity proof.
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("-1").SetURL("/a/one").SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("name:epilogue").SetURL("/b/two").SaveX(ctx)

	negNumber := -1.0
	neg := client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("-1").SetNumber(negNumber).
		SetState(entchapter.StateDownloaded).SetFilename("neg.cbz").SaveX(ctx)
	named := client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("name:epilogue").
		SetState(entchapter.StateDownloaded).SetFilename("named.cbz").SaveX(ctx)

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, sr.ID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (no shared URL ⇒ no provable pair)", removed)
	}
	if !client.Chapter.Query().Where(entchapter.IDEQ(neg.ID)).ExistX(ctx) {
		t.Error("negative chapter was removed without a URL match")
	}
	if !client.Chapter.Query().Where(entchapter.IDEQ(named.ID)).ExistX(ctx) {
		t.Error("named chapter was removed unexpectedly")
	}
}

// TestDedupeFiles_NoMergeWhenURLShardedAcrossThreeChapters proves an AMBIGUOUS URL
// group (a URL carried by more than a clean neg+named pair) is skipped: identity is
// only provable for exactly one negative + one name-keyed chapter.
func TestDedupeFiles_NoMergeWhenURLShardedAcrossThreeChapters(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	storage := t.TempDir()

	sr := client.Series.Create().
		SetTitle("Ambiguous Series").SetSlug("ambiguous-series").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeriesID(sr.ID).SetProvider("101").SetImportance(10).SaveX(ctx)

	url := "/shared/url"
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("-1").SetURL(url).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("name:epilogue").SetURL(url).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey("name:extra").SetURL(url).SaveX(ctx)

	negNumber := -1.0
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("-1").SetNumber(negNumber).
		SetState(entchapter.StateDownloaded).SetFilename("neg.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("name:epilogue").
		SetState(entchapter.StateDownloaded).SetFilename("named.cbz").SaveX(ctx)
	client.Chapter.Create().SetSeriesID(sr.ID).SetChapterKey("name:extra").
		SetState(entchapter.StateDownloaded).SetFilename("extra.cbz").SaveX(ctx)

	svc := series.NewService(client, storage, 14)
	removed, err := svc.DedupeFiles(ctx, sr.ID)
	if err != nil {
		t.Fatalf("DedupeFiles: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0 (ambiguous URL group must be skipped)", removed)
	}
	if n := client.Chapter.Query().CountX(ctx); n != 3 {
		t.Errorf("chapter count = %d, want 3 (nothing merged)", n)
	}
}
