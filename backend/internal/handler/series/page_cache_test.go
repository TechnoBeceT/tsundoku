package series_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// stampDownloadDate gives the seeded chapter (already carrying a filename —
// see testEnv.seed) a download_date, so it has a non-empty page version. Real
// code paths set both together (download/dispatcher.go + download/upgrade.go);
// tests set it explicitly to control the exact version under test.
func stampDownloadDate(ctx context.Context, t *testing.T, env *testEnv, chID uuid.UUID, at time.Time) *ent.Chapter {
	t.Helper()
	return env.client.Chapter.UpdateOneID(chID).SetDownloadDate(at).SaveX(ctx)
}

// TestChapterPage_MatchingVersionIsCacheable proves a request whose ?v=
// matches the chapter's CURRENT page version earns the full-day cache — this
// is the fix the client-side prefetcher depends on: prefetched pages must
// survive until the owner finishes reading, not expire mid-chapter.
func TestChapterPage_MatchingVersionIsCacheable(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)
	ch := stampDownloadDate(ctx, t, env, chID, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	version := seriessvc.PageVersion(ch.Filename, ch.DownloadDate)
	if version == "" {
		t.Fatal("test setup: expected a non-empty page version")
	}

	target := "/api/series/" + env.mangaID.String() + "/chapters/" + chID.String() + "/pages/0?v=" + version
	rec := env.do(http.MethodGet, target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ChapterPage matching ?v=: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=86400" {
		t.Fatalf("ChapterPage matching ?v= cache-control: got %q, want private, max-age=86400", cc)
	}
	if etag := rec.Header().Get("ETag"); etag == "" {
		t.Error("ChapterPage matching ?v=: missing ETag")
	}
}

// TestChapterPage_ImportedChapterWithoutDownloadDateIsCacheable is the FIX 1
// regression proof, at the HTTP layer: the seeded "alpha-1" chapter carries a
// filename (env.seed's SetFilename) but deliberately NO download_date — this
// is exactly the shape `disk.Reconcile` leaves on every disk-imported /
// Kaizoku-migrated chapter, i.e. most of the owner's real library. It must
// NOT be routed through the handler's version=="" branch (no ETag,
// unconditional no-cache — strictly worse than this feature's predecessor):
// with a matching ?v= it must earn the full-day cache AND an ETag, exactly
// like a freshly-downloaded chapter. Deliberately does NOT use
// stampDownloadDate — that helper is what was masking this bug.
func TestChapterPage_ImportedChapterWithoutDownloadDateIsCacheable(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)
	if ch.DownloadDate != nil {
		t.Fatal("test setup: expected the seeded chapter to have NO download_date (the disk.Reconcile shape)")
	}
	version := seriessvc.PageVersion(ch.Filename, ch.DownloadDate)
	if version == "" {
		t.Fatal("PageVersion: want a non-empty version for a filename with no download_date, got empty")
	}

	target := "/api/series/" + env.mangaID.String() + "/chapters/" + chID.String() + "/pages/0?v=" + version
	rec := env.do(http.MethodGet, target, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ChapterPage matching ?v= (no download_date): want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "private, max-age=86400" {
		t.Fatalf("ChapterPage matching ?v= (no download_date) cache-control: got %q, want private, max-age=86400", cc)
	}
	if etag := rec.Header().Get("ETag"); etag == "" {
		t.Error("ChapterPage matching ?v= (no download_date): missing ETag")
	}
}

// TestChapterPage_UnversionedOrStaleNeverLongCaches proves the one case that
// would risk serving stale bytes — no ?v= at all (a bookmark/curl/preload with
// no cache buster) or a STALE ?v= (held from before a convergence upgrade
// replaced the CBZ) — always revalidates instead of getting the long max-age.
func TestChapterPage_UnversionedOrStaleNeverLongCaches(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)
	stampDownloadDate(ctx, t, env, chID, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))

	base := "/api/series/" + env.mangaID.String() + "/chapters/" + chID.String() + "/pages/0"
	for _, target := range []string{base, base + "?v=not-the-current-version"} {
		rec := env.do(http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("ChapterPage %s: want 200, got %d (%s)", target, rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-cache" {
			t.Errorf("ChapterPage %s cache-control: got %q, want private, no-cache", target, cc)
		}
	}
}

// TestChapterPage_IfNoneMatchNeverOpensCBZ is the load-bearing proof: a
// conditional request carrying the CURRENT ETag gets a 304 WITHOUT the CBZ
// ever being opened. The chapter is pointed at a CBZ that does not exist on
// disk at all — a 304 that had to open the (missing) archive would fail with a
// 404, not succeed with a 304, so this test can only pass if the conditional
// check is answered from the DB-only page version alone.
func TestChapterPage_IfNoneMatchNeverOpensCBZ(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	// The seeded alpha-1 chapter already carries a filename (set in seed()),
	// but its CBZ is deliberately never written to disk.
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)
	ch = stampDownloadDate(ctx, t, env, ch.ID, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	version := seriessvc.PageVersion(ch.Filename, ch.DownloadDate)
	if version == "" {
		t.Fatal("test setup: expected a non-empty page version")
	}
	etag := `"` + version + "-0" + `"`

	target := "/api/series/" + env.mangaID.String() + "/chapters/" + ch.ID.String() + "/pages/0"
	rec := env.doWithHeader(target, "If-None-Match", etag)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("ChapterPage If-None-Match (missing CBZ): want 304 (proves the archive was never opened), got %d (%s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("ChapterPage 304: body must be empty, got %d bytes", rec.Body.Len())
	}
}

// TestChapterPage_VersionTracksReplacedCBZ proves the ETag follows the
// chapter's CONTENT identity: a "Library-Convergence upgrade" that re-renders
// the CBZ (simulated here by a fresh download_date) must yield a DIFFERENT
// version and ETag, or a client holding the old one could win a spurious 304
// against pages that have actually changed.
func TestChapterPage_VersionTracksReplacedCBZ(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)
	ch := stampDownloadDate(ctx, t, env, chID, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	oldVersion := seriessvc.PageVersion(ch.Filename, ch.DownloadDate)

	target := "/api/series/" + env.mangaID.String() + "/chapters/" + chID.String() + "/pages/0"
	first := env.do(http.MethodGet, target, "")
	if got := first.Header().Get("ETag"); got != `"`+oldVersion+`-0"` {
		t.Fatalf("ChapterPage first ETag: got %q, want %q", got, `"`+oldVersion+`-0"`)
	}

	// A convergence upgrade re-renders the CBZ and re-stamps download_date.
	ch = stampDownloadDate(ctx, t, env, chID, time.Date(2026, 7, 1, 13, 0, 0, 0, time.UTC))
	newVersion := seriessvc.PageVersion(ch.Filename, ch.DownloadDate)
	if newVersion == oldVersion {
		t.Fatal("test setup: expected the re-stamp to change the version")
	}

	second := env.do(http.MethodGet, target, "")
	if got := second.Header().Get("ETag"); got != `"`+newVersion+`-0"` {
		t.Fatalf("ChapterPage ETag after re-render: got %q, want the NEW version %q", got, `"`+newVersion+`-0"`)
	}

	// The stale ETag from before the re-render must NOT win a 304 anymore.
	rec := env.doWithHeader(target, "If-None-Match", `"`+oldVersion+`-0"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ChapterPage stale If-None-Match: want 200 (a fresh fetch), got %d", rec.Code)
	}
}
