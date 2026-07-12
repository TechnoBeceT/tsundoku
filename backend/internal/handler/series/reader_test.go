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
