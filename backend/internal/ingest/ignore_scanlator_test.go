package ingest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// fakeIgnoreStore is an in-memory ingest.IgnoreScanlatorStore for the tests
// below: `flagged` is the set of source ids the owner has flagged; `err`, when
// set, forces IgnoreScanlatorSet to fail (exercising the best-effort fallback).
type fakeIgnoreStore struct {
	flagged map[int64]bool
	err     error
}

func (f fakeIgnoreStore) IgnoreScanlatorSet(context.Context) (map[int64]bool, error) {
	return f.flagged, f.err
}

// ignoreScanlatorFixture builds the shared uploader-in-scanlator manga fixture:
// a source ("Hive Scans") whose chapters are tagged by UPLOADER (Admin, Aero),
// exactly the shape the ignore-scanlator flag exists to collapse. Returns the
// engine fake plus the two per-uploader chapter slices.
func ignoreScanlatorFixture(t *testing.T, sourceID int64, mangaURL, mangaTitle string) (*enginefake.Client, []sourceengine.Chapter, []sourceengine.Chapter) {
	t.Helper()
	admin := makeChaptersWithScanlator(2, 1, "Admin")
	aero := makeChaptersWithScanlator(2, 3, "Aero")
	all := append(append([]sourceengine.Chapter{}, admin...), aero...)
	fc := enginefake.New(
		enginefake.WithChapters(sourceID, mangaURL, all),
		enginefake.WithMangaDetails(sourceID, mangaURL, sourceengine.MangaDetails{Title: mangaTitle}),
		enginefake.WithSources([]sourceengine.Source{{ID: sourceID, Name: "Hive Scans"}}),
	)
	return fc, admin, aero
}

// TestIngest_AddSeries_IgnoreScanlator_CollapsesToEmpty proves the core Slice A
// behaviour: when a source is flagged ignore-scanlator, an adopt under a
// per-uploader scanlator is forced to "" — so ONE [Source] provider is created
// carrying EVERY uploader's chapters (Admin + Aero), instead of a fake
// per-uploader row holding only Admin's.
func TestIngest_AddSeries_IgnoreScanlator_CollapsesToEmpty(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 2001
		mangaURL         = "/manga/hive-collapse"
		mangaTitle       = "Hive Collapse Manga"
	)
	fc, admin, aero := ignoreScanlatorFixture(t, sourceID, mangaURL, mangaTitle)
	all := append(append([]sourceengine.Chapter{}, admin...), aero...)

	ing := ingest.NewIngest(fc, client).
		WithIgnoreScanlator(fakeIgnoreStore{flagged: map[int64]bool{sourceID: true}})

	// Adopt under a per-uploader scanlator — the flag must force it to "".
	res, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "Admin")
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Scanlator != "" {
		t.Errorf("SeriesProvider.Scanlator got %q, want \"\" (flagged source must collapse)", sp.Scanlator)
	}
	if res.NewProviderChapters != len(all) {
		t.Errorf("NewProviderChapters got %d, want %d (all uploaders merged into one provider)", res.NewProviderChapters, len(all))
	}
	assertProviderChapterURLs(t, ctx, client, sp.ID, buildWantURLs(all))
}

// TestIngest_AddSeries_IgnoreScanlator_UnflaggedStillSplits is the zero-disruption
// regression proof: with the SAME fixture but the source NOT flagged, the
// scanlator-aware split is untouched — an adopt under "Admin" creates an
// "Admin"-scoped provider carrying ONLY Admin's chapters.
func TestIngest_AddSeries_IgnoreScanlator_UnflaggedStillSplits(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 2002
		mangaURL         = "/manga/hive-split"
		mangaTitle       = "Hive Split Manga"
	)
	fc, admin, _ := ignoreScanlatorFixture(t, sourceID, mangaURL, mangaTitle)

	ing := ingest.NewIngest(fc, client).
		WithIgnoreScanlator(fakeIgnoreStore{flagged: map[int64]bool{}}) // nothing flagged

	res, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "Admin")
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Scanlator != "Admin" {
		t.Errorf("SeriesProvider.Scanlator got %q, want \"Admin\" (unflagged source must keep its split)", sp.Scanlator)
	}
	if res.NewProviderChapters != len(admin) {
		t.Errorf("NewProviderChapters got %d, want %d (only Admin's chapters)", res.NewProviderChapters, len(admin))
	}
	assertProviderChapterURLs(t, ctx, client, sp.ID, buildWantURLs(admin))
}

// TestIngest_AddSeriesWithChapters_IgnoreScanlator_KeepsStoredScanlator is the
// Slice A invariant proof: the REFRESH entry (AddSeriesWithChapters) must NEVER
// collapse, even when the source is flagged — it passes the STORED scanlator so
// an already-adopted per-uploader row keeps refreshing its OWN row and is never
// migrated. Here a flagged source's stored "Admin" row re-ingests as "Admin"
// (not ""), holding only Admin's chapters.
func TestIngest_AddSeriesWithChapters_IgnoreScanlator_KeepsStoredScanlator(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 2003
		mangaURL         = "/manga/hive-refresh"
		mangaTitle       = "Hive Refresh Manga"
	)
	fc, admin, aero := ignoreScanlatorFixture(t, sourceID, mangaURL, mangaTitle)
	all := append(append([]sourceengine.Chapter{}, admin...), aero...)

	ing := ingest.NewIngest(fc, client).
		WithIgnoreScanlator(fakeIgnoreStore{flagged: map[int64]bool{sourceID: true}})

	// The refresh sweep hands the raw all-scanlators list back with the STORED
	// per-uploader scanlator; the flag must NOT collapse it (apply-forward only).
	res, err := ing.AddSeriesWithChapters(ctx, sourceID, mangaURL, mangaTitle, "Admin", all)
	if err != nil {
		t.Fatalf("AddSeriesWithChapters: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Scanlator != "Admin" {
		t.Errorf("SeriesProvider.Scanlator got %q, want \"Admin\" (refresh must keep stored scanlator — Slice A)", sp.Scanlator)
	}
	if res.NewProviderChapters != len(admin) {
		t.Errorf("NewProviderChapters got %d, want %d (refresh keeps the per-uploader feed intact)", res.NewProviderChapters, len(admin))
	}
	assertProviderChapterURLs(t, ctx, client, sp.ID, buildWantURLs(admin))
}

// TestIngest_AddSeries_IgnoreScanlator_ReadErrorKeepsSplit proves the
// best-effort contract: a flag-store read failure must NOT fail the ingest and
// must fall back to the requested scanlator (the split is preserved rather than
// a spurious collapse from an unreadable flag).
func TestIngest_AddSeries_IgnoreScanlator_ReadErrorKeepsSplit(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)

	const (
		sourceID   int64 = 2004
		mangaURL         = "/manga/hive-readerr"
		mangaTitle       = "Hive ReadErr Manga"
	)
	fc, admin, _ := ignoreScanlatorFixture(t, sourceID, mangaURL, mangaTitle)

	ing := ingest.NewIngest(fc, client).
		WithIgnoreScanlator(fakeIgnoreStore{err: errors.New("db down")})

	res, err := ing.AddSeries(ctx, sourceID, mangaURL, mangaTitle, "Admin")
	if err != nil {
		t.Fatalf("AddSeries must not fail on a flag-store read error: %v", err)
	}

	sp := client.SeriesProvider.Query().OnlyX(ctx)
	if sp.Scanlator != "Admin" {
		t.Errorf("SeriesProvider.Scanlator got %q, want \"Admin\" (read error must keep the requested scanlator)", sp.Scanlator)
	}
	if res.NewProviderChapters != len(admin) {
		t.Errorf("NewProviderChapters got %d, want %d", res.NewProviderChapters, len(admin))
	}
}

// TestIngest_EffectiveScanlator_NilReceiverAndStore proves the nil-safe
// best-effort primitives: a nil *Ingest and a nil store both leave the requested
// scanlator unchanged (never a panic, never a spurious collapse).
func TestIngest_EffectiveScanlator_NilReceiverAndStore(t *testing.T) {
	ctx := context.Background()

	var nilIngest *ingest.Ingest
	if got := nilIngest.EffectiveScanlator(ctx, 1, "Admin"); got != "Admin" {
		t.Errorf("nil-receiver EffectiveScanlator got %q, want \"Admin\"", got)
	}
	if nilIngest.IgnoreScanlator(ctx, 1) {
		t.Error("nil-receiver IgnoreScanlator got true, want false")
	}

	noStore := ingest.NewIngest(enginefake.New(), (*ent.Client)(nil))
	if got := noStore.EffectiveScanlator(ctx, 1, "Admin"); got != "Admin" {
		t.Errorf("no-store EffectiveScanlator got %q, want \"Admin\"", got)
	}
	// An empty requested scanlator is always "" regardless of the flag.
	if got := noStore.EffectiveScanlator(ctx, 1, ""); got != "" {
		t.Errorf("empty scanlator EffectiveScanlator got %q, want \"\"", got)
	}
}
