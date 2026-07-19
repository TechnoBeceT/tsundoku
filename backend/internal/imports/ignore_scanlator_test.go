package imports_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// fakeIgnoreScanlator is an in-memory ingest.IgnoreScanlatorStore for the
// imports-layer tests: `flagged` is the set of source ids flagged
// ignore-scanlator.
type fakeIgnoreScanlator struct {
	flagged map[int64]bool
}

func (f fakeIgnoreScanlator) IgnoreScanlatorSet(context.Context) (map[int64]bool, error) {
	return f.flagged, nil
}

// TestService_SourceBreakdown_IgnoreScanlator_CollapsesToOneRow proves the
// breakdown collapses a FLAGGED source into a single [Source]-name row carrying
// EVERY chapter, so the Adopt UI shows one provider row (not one per uploader).
// Contrast TestService_SourceBreakdown_GroupsByScanlator, which keeps the split
// for an unflagged source.
func TestService_SourceBreakdown_IgnoreScanlator_CollapsesToOneRow(t *testing.T) {
	t.Parallel()

	const (
		sourceID   int64 = 1
		sourceName       = "Hive Scans"
		url              = "/manga/hive"
	)
	fc := &fakeClient{
		sources: []sourceengine.Source{{ID: sourceID, Name: sourceName, Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{
			url: {
				{URL: "/ch/1", Number: 1, Scanlator: "Admin"},
				{URL: "/ch/2", Number: 2, Scanlator: "Aero"},
				{URL: "/ch/3", Number: 3, Scanlator: ""},
			},
		},
	}
	ingestSvc := ingest.NewIngest(fc, nil).
		WithIgnoreScanlator(fakeIgnoreScanlator{flagged: map[int64]bool{sourceID: true}})
	svc := imports.NewService(fc, ingestSvc, nil, "", testSearchTimeout, nil)

	got, err := svc.SourceBreakdown(context.Background(), "1", url, "")
	if err != nil {
		t.Fatalf("SourceBreakdown: %v", err)
	}
	if got.Total != 3 {
		t.Errorf("Total = %d, want 3", got.Total)
	}
	if len(got.Scanlators) != 1 {
		t.Fatalf("got %d groups, want 1 (flagged source must collapse)", len(got.Scanlators))
	}
	g := got.Scanlators[0]
	if g.Scanlator != sourceName {
		t.Errorf("group key = %q, want %q (the source name)", g.Scanlator, sourceName)
	}
	if g.Count != 3 {
		t.Errorf("group Count = %d, want 3 (all uploaders merged)", g.Count)
	}
	if g.Ranges != "1-3" {
		t.Errorf("group Ranges = %q, want \"1-3\"", g.Ranges)
	}
}

// TestService_Adopt_IgnoreScanlator_CollapsesProvider proves an Adopt of a
// flagged source under a per-uploader scanlator collapses to ONE
// scanlator="" SeriesProvider carrying every chapter, AND that the requested
// importance still lands on it — setImportances must key the collapsed row, not
// the stale "Admin" scanlator the request carried.
func TestService_Adopt_IgnoreScanlator_CollapsesProvider(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle       = "Hive Manga"
		sourceID       int64 = 1
		src                  = "1"
		url                  = "/manga/hive"
		imp                  = 10
	)
	// Chapters tagged by uploader (Admin, Aero) — the split a flagged source
	// must collapse.
	chapters := []sourceengine.Chapter{
		{Name: "Chapter 1", Number: 1, URL: url + "/ch/1", Scanlator: "Admin"},
		{Name: "Chapter 2", Number: 2, URL: url + "/ch/2", Scanlator: "Aero"},
		{Name: "Chapter 3", Number: 3, URL: url + "/ch/3", Scanlator: "Admin"},
	}
	fc := &fakeClient{
		sources:       []sourceengine.Source{{ID: sourceID, Name: "Hive Scans", Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{url: chapters},
	}
	ingestSvc := ingest.NewIngest(fc, db).
		WithIgnoreScanlator(fakeIgnoreScanlator{flagged: map[int64]bool{sourceID: true}})
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

	// Adopt under a stale per-uploader scanlator (a stale FE) — the flag forces "".
	id, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: src, URL: url, Importance: imp, Scanlator: "Admin"},
		},
	})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	sps := db.SeriesProvider.Query().AllX(ctx)
	if len(sps) != 1 {
		t.Fatalf("SeriesProvider count = %d, want 1 (flagged source collapses to one provider)", len(sps))
	}
	sp := sps[0]
	if sp.Scanlator != "" {
		t.Errorf("SeriesProvider.Scanlator = %q, want \"\" (collapsed)", sp.Scanlator)
	}
	if sp.Importance != imp {
		t.Errorf("SeriesProvider.Importance = %d, want %d (importance must land on the collapsed row)", sp.Importance, imp)
	}
	// All three chapters (both uploaders) are ingested onto the single provider.
	assertAdoptChapters(t, ctx, db, 3)
	_ = id
}

// TestService_Adopt_IgnoreScanlator_SameSourceTwoUploadersFoldToOne proves NOTE
// 4: adopting the SAME flagged source twice under two different uploader
// scanlators ("Admin" + "Aero") collapses both to (src,"") and folds them into
// ONE SeriesProvider row — the last provider's importance wins. This is the
// intended behaviour for a flagged source (it has exactly one real provider).
func TestService_Adopt_IgnoreScanlator_SameSourceTwoUploadersFoldToOne(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)

	const (
		canonicalTitle       = "Hive Manga"
		sourceID       int64 = 1
		src                  = "1"
		url                  = "/manga/hive"
	)
	chapters := []sourceengine.Chapter{
		{Name: "Chapter 1", Number: 1, URL: url + "/ch/1", Scanlator: "Admin"},
		{Name: "Chapter 2", Number: 2, URL: url + "/ch/2", Scanlator: "Aero"},
	}
	fc := &fakeClient{
		sources:       []sourceengine.Source{{ID: sourceID, Name: "Hive Scans", Lang: "en"}},
		chaptersByURL: map[string][]sourceengine.Chapter{url: chapters},
	}
	ingestSvc := ingest.NewIngest(fc, db).
		WithIgnoreScanlator(fakeIgnoreScanlator{flagged: map[int64]bool{sourceID: true}})
	svc := imports.NewService(fc, ingestSvc, db, "", testSearchTimeout, nil)

	// Two providers, SAME source, different uploader scanlators. impAero (the
	// last) must win on the single collapsed row.
	const impAdmin, impAero = 10, 4
	if _, err := svc.Adopt(ctx, imports.AdoptRequest{
		Title: canonicalTitle,
		Providers: []imports.AdoptProvider{
			{Source: src, URL: url, Importance: impAdmin, Scanlator: "Admin"},
			{Source: src, URL: url, Importance: impAero, Scanlator: "Aero"},
		},
	}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}

	sps := db.SeriesProvider.Query().AllX(ctx)
	if len(sps) != 1 {
		t.Fatalf("SeriesProvider count = %d, want 1 (both uploaders fold into one collapsed row)", len(sps))
	}
	if sps[0].Scanlator != "" {
		t.Errorf("SeriesProvider.Scanlator = %q, want \"\" (collapsed)", sps[0].Scanlator)
	}
	if sps[0].Importance != impAero {
		t.Errorf("SeriesProvider.Importance = %d, want %d (last uploader wins)", sps[0].Importance, impAero)
	}
	// Both chapters (Admin's + Aero's) live on the single provider.
	assertAdoptChapters(t, ctx, db, 2)
}
