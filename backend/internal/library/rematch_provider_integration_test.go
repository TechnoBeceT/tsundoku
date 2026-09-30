package library_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	enginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

const (
	rematchOldURL = "/series/old"
	rematchNewURL = "/comics/new"
)

type rematchFixture struct {
	ctx                  context.Context
	db                   *ent.Client
	service              *library.Service
	hub                  *sse.Hub
	storage              string
	seriesID, providerID uuid.UUID
	file                 string
}
type rematchSnapshot struct {
	url             string
	chapters, feeds int
	file            string
}

func newRematchFixture(t *testing.T) rematchFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.New(t)
	storage := t.TempDir()
	engine := rematchEngine()
	hub := sse.NewHub()
	svc := library.NewService(db, ingest.NewIngest(engine, db), nil, series.NewService(db, storage, 14), func() {}, storage, hub).WithSourceLister(engine)
	row := db.Series.Create().SetTitle("Rematch Series").SetSlug("rematch-series").SaveX(ctx)
	if _, err := svc.AddProviderRef(ctx, row.ID, library.ProviderRef{Source: "1", URL: rematchOldURL, AddressMode: sourceengine.AddressModeDirect}, 17); err != nil {
		t.Fatal(err)
	}
	sp := db.SeriesProvider.Query().Where(seriesprovider.SeriesID(row.ID)).OnlyX(ctx)
	ch := db.Chapter.Query().Where(chapter.SeriesID(row.ID), chapter.ChapterKeyEQ("1")).OnlyX(ctx)
	db.Chapter.UpdateOne(ch).SetState(chapter.StateDownloaded).SetFilename("kept.cbz").SetSatisfiedByProviderID(sp.ID).SetSatisfiedImportance(17).SaveX(ctx)
	feed := db.ProviderChapter.Query().Where(providerchapter.SeriesProviderID(sp.ID), providerchapter.ChapterKeyEQ("1")).OnlyX(ctx)
	db.ProviderChapter.UpdateOne(feed).SetAttempts(4).SetLastError("keep").SaveX(ctx)
	file := filepath.Join(storage, "kept.cbz")
	if err := os.WriteFile(file, []byte("unchanged cbz"), 0o600); err != nil {
		t.Fatal(err)
	}
	return rematchFixture{ctx: ctx, db: db, service: svc, hub: hub, storage: storage, seriesID: row.ID, providerID: sp.ID, file: file}
}

func rematchEngine(opts ...enginefake.Option) *enginefake.Client {
	base := []enginefake.Option{enginefake.WithSources([]sourceengine.Source{{ID: 1, Name: "Current Source", Lang: "en"}}), enginefake.WithMangaDetails(1, rematchOldURL, sourceengine.MangaDetails{URL: rematchOldURL, Title: "Rematch Series"}), enginefake.WithMangaDetails(1, rematchNewURL, sourceengine.MangaDetails{URL: rematchNewURL, Title: "Rematch Series", RealURL: "https://source.test/comics/new"}), enginefake.WithChapters(1, rematchOldURL, []sourceengine.Chapter{{URL: "/chapter/1-old", Name: "Chapter 1", Number: 1}, {URL: "/chapter/2", Name: "Chapter 2", Number: 2}}), enginefake.WithChapters(1, rematchNewURL, []sourceengine.Chapter{{URL: "/chapter/1-new", Name: "Chapter 1", Number: 1}, {URL: "/chapter/3", Name: "Chapter 3", Number: 3}})}
	return enginefake.New(append(base, opts...)...)
}

func TestRecoverProviderAddressPreservesDownloadedChapterAndFile(t *testing.T) {
	f := newRematchFixture(t)
	f.db.Chapter.Create().SetSeriesID(f.seriesID).SetChapterKey("3").SaveX(f.ctx)
	f.db.ProviderChapter.Create().SetSeriesProviderID(f.providerID).SetChapterKey("3").SetURL("/chapter/3-old").SetProviderIndex(2).SaveX(f.ctx)
	before := f.snapshot(t)
	downloaded := f.db.Chapter.Query().Where(chapter.SeriesID(f.seriesID), chapter.ChapterKeyEQ("1")).OnlyX(f.ctx)
	candidate := sourceengine.MangaEntry{URL: rematchNewURL, Title: "Rematch Series", AddressMode: sourceengine.AddressModeDirect}
	engine := rematchEngine(
		enginefake.WithSearchResult(1, sourceengine.SearchResult{Manga: []sourceengine.MangaEntry{candidate}}),
		enginefake.WithChapters(1, rematchNewURL, []sourceengine.Chapter{{URL: "/chapter/1-new", Number: 1}, {URL: "/chapter/2-new", Number: 2}, {URL: "/chapter/3-new", Number: 3}, {URL: "/chapter/4-new", Number: 4}}),
	)
	f.replaceEngine(engine, true)
	paceCalls := 0
	if err := f.service.RecoverProviderAddress(f.ctx, f.providerID, func(context.Context) { paceCalls++ }); err != nil {
		t.Fatal(err)
	}
	if paceCalls != 3 {
		t.Fatalf("paced calls = %d, want search, details, chapters", paceCalls)
	}
	after := f.snapshot(t)
	assertRecoveredState(t, f, before, after, downloaded)
}

func assertRecoveredState(t *testing.T, f rematchFixture, before, after rematchSnapshot, downloaded *ent.Chapter) {
	t.Helper()
	if after.url != rematchNewURL || after.file != before.file || after.feeds != before.feeds+1 || after.chapters != before.chapters+1 {
		t.Fatalf("recovery changed unexpected state: before=%+v after=%+v", before, after)
	}
	got := f.db.Chapter.GetX(f.ctx, downloaded.ID)
	if got.State != chapter.StateDownloaded || got.Filename != downloaded.Filename || got.SatisfiedImportance == nil || downloaded.SatisfiedImportance == nil || *got.SatisfiedImportance != *downloaded.SatisfiedImportance {
		t.Fatalf("downloaded chapter changed: %+v", got)
	}
}
func (f *rematchFixture) replaceEngine(engine *enginefake.Client, withLister bool) {
	f.service = library.NewService(f.db, ingest.NewIngest(engine, f.db), nil, series.NewService(f.db, f.storage, 14), func() {}, f.storage, f.hub)
	if withLister {
		f.service.WithSourceLister(engine)
	}
}
func (f rematchFixture) snapshot(t *testing.T) rematchSnapshot {
	t.Helper()
	b, err := os.ReadFile(f.file)
	if err != nil {
		t.Fatal(err)
	}
	return rematchSnapshot{url: f.db.SeriesProvider.GetX(f.ctx, f.providerID).URL, chapters: f.db.Chapter.Query().Where(chapter.SeriesID(f.seriesID)).CountX(f.ctx), feeds: f.db.ProviderChapter.Query().Where(providerchapter.SeriesProviderID(f.providerID)).CountX(f.ctx), file: string(b)}
}

func TestRematchProviderPreservesIdentityFilesAndMissingFeedWhileAddingWanted(t *testing.T) {
	f := newRematchFixture(t)
	sp0 := f.db.SeriesProvider.GetX(f.ctx, f.providerID)
	ch0 := f.db.Chapter.Query().Where(chapter.SeriesID(f.seriesID), chapter.ChapterKeyEQ("1")).OnlyX(f.ctx)
	pc0 := f.db.ProviderChapter.Query().Where(providerchapter.SeriesProviderID(f.providerID), providerchapter.ChapterKeyEQ("1")).OnlyX(f.ctx)
	before := f.snapshot(t)
	if _, err := f.service.RematchProvider(f.ctx, f.seriesID, f.providerID, library.ProviderRef{Source: "1", URL: rematchNewURL, AddressMode: sourceengine.AddressModeDirect}); err != nil {
		t.Fatal(err)
	}
	assertSuccessfulRematch(t, f, before, sp0, ch0, pc0)
}

func assertSuccessfulRematch(t *testing.T, f rematchFixture, before rematchSnapshot, sp0 *ent.SeriesProvider, ch0 *ent.Chapter, pc0 *ent.ProviderChapter) {
	t.Helper()
	if after := f.snapshot(t); before.file != after.file {
		t.Fatal("CBZ bytes changed")
	}
	assertRematchProviderIdentity(t, f, sp0)
	assertRematchChapterIdentity(t, f, ch0)
	assertRematchFeedIdentity(t, f, pc0)
	assertRematchFeedShape(t, f)
}

func assertRematchProviderIdentity(t *testing.T, f rematchFixture, before *ent.SeriesProvider) {
	t.Helper()
	sp := f.db.SeriesProvider.GetX(f.ctx, f.providerID)
	if sp.ID != before.ID || sp.Importance != before.Importance || sp.Scanlator != before.Scanlator {
		t.Fatal("provider identity/settings changed")
	}
}
func assertRematchChapterIdentity(t *testing.T, f rematchFixture, before *ent.Chapter) {
	t.Helper()
	ch := f.db.Chapter.GetX(f.ctx, before.ID)
	if ch.State != chapter.StateDownloaded || ch.Filename != "kept.cbz" || ch.SatisfiedByProviderID == nil || *ch.SatisfiedByProviderID != f.providerID {
		t.Fatal("downloaded chapter changed")
	}
}
func assertRematchFeedIdentity(t *testing.T, f rematchFixture, before *ent.ProviderChapter) {
	t.Helper()
	pc := f.db.ProviderChapter.GetX(f.ctx, before.ID)
	if pc.Attempts != 4 || pc.LastError != "keep" {
		t.Fatal("retry state changed")
	}
}
func assertRematchFeedShape(t *testing.T, f rematchFixture) {
	t.Helper()
	if !f.db.ProviderChapter.Query().Where(providerchapter.SeriesProviderID(f.providerID), providerchapter.ChapterKeyEQ("2")).ExistX(f.ctx) {
		t.Fatal("missing upstream chapter deleted")
	}
	if got := f.db.Chapter.Query().Where(chapter.SeriesID(f.seriesID), chapter.ChapterKeyEQ("3")).OnlyX(f.ctx).State; got != chapter.StateWanted {
		t.Fatalf("new chapter state=%s", got)
	}
}

func TestRematchProviderPreflightFailuresAreNoOps(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*rematchFixture)
		ref       library.ProviderRef
		want      error
	}{{"source mismatch", nil, library.ProviderRef{Source: "2", URL: rematchNewURL}, library.ErrProviderSourceMismatch}, {"source lister absent", func(f *rematchFixture) { f.replaceEngine(rematchEngine(), false) }, library.ProviderRef{Source: "1", URL: rematchNewURL}, library.ErrSourceUnavailable}, {"source uninstalled", func(f *rematchFixture) { f.replaceEngine(rematchEngine(enginefake.WithSources(nil)), true) }, library.ProviderRef{Source: "1", URL: rematchNewURL}, library.ErrSourceNotFound}, {"source list failure", func(f *rematchFixture) {
		f.replaceEngine(rematchEngine(enginefake.WithError("Sources", errors.New("registry down"))), true)
	}, library.ProviderRef{Source: "1", URL: rematchNewURL}, library.ErrSourceUpstream}, {"details failure", func(f *rematchFixture) {
		f.replaceEngine(rematchEngine(enginefake.WithError("MangaDetails", errors.New("details down"))), true)
	}, library.ProviderRef{Source: "1", URL: rematchNewURL}, library.ErrSourceUpstream}, {"chapters failure", func(f *rematchFixture) {
		f.replaceEngine(rematchEngine(enginefake.WithError("Chapters", errors.New("chapters down"))), true)
	}, library.ProviderRef{Source: "1", URL: rematchNewURL}, library.ErrSourceUpstream}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRematchFixture(t)
			if tt.configure != nil {
				tt.configure(&f)
			}
			before := f.snapshot(t)
			_, err := f.service.RematchProvider(f.ctx, f.seriesID, f.providerID, tt.ref)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error=%v want=%v", err, tt.want)
			}
			if after := f.snapshot(t); before != after {
				t.Fatalf("state changed: before=%v after=%v", before, after)
			}
		})
	}
}

func TestRematchProviderRejectsCrossSeriesUnlinkedAndBusyWithoutMutation(t *testing.T) {
	f := newRematchFixture(t)
	other := f.db.Series.Create().SetTitle("Other").SetSlug("other").SaveX(f.ctx)
	before := f.snapshot(t)
	if _, err := f.service.RematchProvider(f.ctx, other.ID, f.providerID, library.ProviderRef{Source: "1", URL: rematchNewURL}); !errors.Is(err, library.ErrProviderNotInSeries) {
		t.Fatalf("cross-series=%v", err)
	}
	unlinked := f.db.SeriesProvider.Create().SetSeriesID(f.seriesID).SetProvider("disk-source").SaveX(f.ctx)
	if _, err := f.service.RematchProvider(f.ctx, f.seriesID, unlinked.ID, library.ProviderRef{Source: "1", URL: rematchNewURL}); !errors.Is(err, library.ErrSourceNotFound) {
		t.Fatalf("unlinked=%v", err)
	}
	block := make(chan struct{})
	restore := library.SetConsolidateBlock(block)
	defer restore()
	events, unsubscribe := f.hub.Subscribe()
	defer unsubscribe()
	if !f.service.StartConsolidateProviders(f.ctx, f.seriesID, nil, library.ConsolidateTarget{}) {
		t.Fatal("start latch holder")
	}
	_, err := f.service.RematchProvider(f.ctx, f.seriesID, f.providerID, library.ProviderRef{Source: "1", URL: rematchNewURL})
	close(block)
	<-events
	if !errors.Is(err, library.ErrMergeInFlight) {
		t.Fatalf("busy=%v", err)
	}
	if after := f.snapshot(t); before != after {
		t.Fatalf("state changed: before=%v after=%v", before, after)
	}
}
