package series_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/disk"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// readerPages are the fixed page blobs for the seeded chapter's CBZ; jpeg magic
// so the content type resolves to image/jpeg deterministically.
var readerPages = []fetcher.PageImage{
	{Data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x01}, Ext: "jpg"},
	{Data: []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x02}, Ext: "jpg"},
}

// writeSeededCBZ renders the seeded downloaded chapter's CBZ to the on-disk
// layout (<storage>/Manga/Alpha Saga/<filename>) so the page-bytes endpoint has
// a real archive to read. Returns the chapter id.
func writeSeededCBZ(ctx context.Context, t *testing.T, env *testEnv) uuid.UUID {
	t.Helper()
	ch := env.client.Chapter.Query().
		Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)
	path := disk.ChapterCBZPath(env.storage, "Manga", "Alpha Saga", ch.Filename)
	ci := disk.ComicInfo{Title: "The Beginning", Series: "Alpha Saga", Number: "1", PageCount: len(readerPages)}
	if err := disk.CreateCBZ(path, readerPages, ci); err != nil {
		t.Fatalf("CreateCBZ: %v", err)
	}
	return ch.ID
}

func TestChapterPage_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/chapters/"+chID.String()+"/pages/1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ChapterPage: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("ChapterPage content-type: want image/jpeg, got %q", ct)
	}
	// No ?v= on this request, so it must REVALIDATE: an unversioned URL carries no
	// cache buster, and a convergence upgrade can replace a chapter's bytes at the
	// same id — caching it would be unfixable. The versioned (max-age) case is
	// covered in page_cache_test.go.
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-cache" {
		t.Fatalf("ChapterPage cache-control: got %q", cc)
	}
	if got := rec.Body.Bytes(); len(got) != len(readerPages[1].Data) || got[4] != 0x02 {
		t.Fatalf("ChapterPage bytes: want page 1 blob, got %v", got)
	}
}

// TestChapterPage_UpgradeAvailableServesPages proves the reader page-bytes
// endpoint is NOT state-gated to `downloaded`: a chapter flipped to
// `upgrade_available` (pending a convergence upgrade) still has its old
// source's CBZ + filename intact on disk, so it must still serve its pages —
// the exact bug where a chapter parked in upgrade_available (e.g. a whole
// series stalled by a source ban) became unreadable. The endpoint addresses
// the CBZ by filename alone (see series.ChapterPage), so flipping only the
// state must change nothing.
func TestChapterPage_UpgradeAvailableServesPages(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)
	// Move the chapter out of `downloaded` into `upgrade_available` — its
	// filename/CBZ are untouched (an upgrade deletes the old CBZ only AFTER the
	// new one succeeds).
	env.client.Chapter.UpdateOneID(chID).SetState("upgrade_available").ExecX(ctx)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/chapters/"+chID.String()+"/pages/1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ChapterPage upgrade_available: want 200 (CBZ still on disk), got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("ChapterPage upgrade_available content-type: want image/jpeg, got %q", ct)
	}
	if got := rec.Body.Bytes(); len(got) != len(readerPages[1].Data) || got[4] != 0x02 {
		t.Fatalf("ChapterPage upgrade_available bytes: want page 1 blob, got %v", got)
	}

	// The version/ETag fast path is state-blind too: a matching ?v= on an
	// upgrade_available chapter earns the full-day cache + an ETag, exactly like
	// a downloaded one (the seeded chapter carries a filename but no
	// download_date — the disk.Reconcile shape — which still versions).
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)
	version := seriessvc.PageVersion(ch.Filename, ch.DownloadDate)
	if version == "" {
		t.Fatal("test setup: expected a non-empty page version for the upgrade_available chapter")
	}
	target := "/api/series/" + env.mangaID.String() + "/chapters/" + chID.String() + "/pages/0?v=" + version
	verRec := env.do(http.MethodGet, target, "")
	if verRec.Code != http.StatusOK {
		t.Fatalf("ChapterPage upgrade_available matching ?v=: want 200, got %d (%s)", verRec.Code, verRec.Body.String())
	}
	if cc := verRec.Header().Get("Cache-Control"); cc != "private, max-age=86400" {
		t.Fatalf("ChapterPage upgrade_available matching ?v= cache-control: got %q, want private, max-age=86400", cc)
	}
	wantETag := `"` + version + `-0"`
	if etag := verRec.Header().Get("ETag"); etag != wantETag {
		t.Fatalf("ChapterPage upgrade_available ETag: got %q, want %q", etag, wantETag)
	}
}

// TestChapterPage_UpgradingServesPages is the sibling proof for the `upgrading`
// state — the upgrade fetch is in flight, the old CBZ is likewise still on disk,
// so pages must still serve.
func TestChapterPage_UpgradingServesPages(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)
	env.client.Chapter.UpdateOneID(chID).SetState("upgrading").ExecX(ctx)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/chapters/"+chID.String()+"/pages/0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("ChapterPage upgrading: want 200 (CBZ still on disk), got %d (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.Bytes(); len(got) != len(readerPages[0].Data) || got[4] != 0x01 {
		t.Fatalf("ChapterPage upgrading bytes: want page 0 blob, got %v", got)
	}
}

func TestChapterPage_OutOfRange(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/chapters/"+chID.String()+"/pages/99", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ChapterPage out-of-range: want 404, got %d", rec.Code)
	}
}

func TestChapterPage_MissingCBZ(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	// Do NOT write the CBZ to disk — the DB says downloaded but no file exists.
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/chapters/"+ch.ID.String()+"/pages/0", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ChapterPage missing cbz: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestChapterPage_CorruptCBZ(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)

	// Write garbage (not a valid zip) at the CBZ path: the file exists but cannot
	// be opened as an archive → ErrPageRead → 502.
	path := disk.ChapterCBZPath(env.storage, "Manga", "Alpha Saga", ch.Filename)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not a zip file"), 0o600); err != nil {
		t.Fatalf("write garbage cbz: %v", err)
	}

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/chapters/"+ch.ID.String()+"/pages/0", "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("ChapterPage corrupt cbz: want 502, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestChapterPage_BadIndex(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/chapters/"+chID.String()+"/pages/abc", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ChapterPage bad index: want 400, got %d", rec.Code)
	}
}

func TestChapterPage_WrongSeries(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	chID := writeSeededCBZ(ctx, t, env)

	// A REAL chapter id requested under the WRONG series (Beta Quest owns no such
	// chapter) must 404 — the "chapter belongs to this series" guard, not a leak
	// of another series' pages.
	rec := env.do(http.MethodGet, "/api/series/"+env.manhwaID.String()+"/chapters/"+chID.String()+"/pages/0", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("ChapterPage wrong series: want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestChapterPage_Unauthenticated(t *testing.T) {
	env := newTestEnv(t)
	rec := env.doUnauth(http.MethodGet, "/api/series/"+uuid.New().String()+"/chapters/"+uuid.New().String()+"/pages/0")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ChapterPage unauth: want 401, got %d", rec.Code)
	}
}
