package series_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// seedSourcelessCleanup builds one series with a LIVE source (comix) and the
// chapters the removable rule has to tell apart:
//
//	5 — whole, downloaded, CARRIED by comix   → never removable
//	7 — whole, downloaded, carried by NOBODY  → REMOVABLE (a real CBZ is written for it)
//
// It returns the series id and the two chapters.
func seedSourcelessCleanup(ctx context.Context, t *testing.T, env *testEnv) (seriesID uuid.UUID, removable, protected *ent.Chapter) {
	t.Helper()
	client := env.client

	s := client.Series.Create().
		SetTitle("Sourceless Saga").SetSlug("sourceless-saga").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	live := client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("comix").SetImportance(60).SaveX(ctx)

	client.ProviderChapter.Create().
		SetSeriesProviderID(live.ID).SetChapterKey("5").SetNumber(5).SaveX(ctx)

	dir := filepath.Join(env.storage, "Manga", "Sourceless Saga")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	downloaded := func(key string, n float64, pages int, sp *ent.SeriesProvider) *ent.Chapter {
		filename := key + ".cbz"
		if err := os.WriteFile(filepath.Join(dir, filename), []byte("cbz"), 0o600); err != nil {
			t.Fatalf("write cbz: %v", err)
		}
		return client.Chapter.Create().
			SetSeriesID(s.ID).SetChapterKey(key).SetNumber(n).SetPageCount(pages).
			SetState("downloaded").SetFilename(filename).
			SetSatisfiedByProviderID(sp.ID).SaveX(ctx)
	}
	protected = downloaded("5", 5, 90, live)
	removable = downloaded("7", 7, 5, live)
	return s.ID, removable, protected
}

// TestSourcelessCleanupPreview_OK: GET returns the removable set (chapters no
// remaining source carries) and excludes the chapter comix still carries.
func TestSourcelessCleanupPreview_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, removable, protected := seedSourcelessCleanup(ctx, t, env)

	rec := env.do(http.MethodGet, "/api/series/"+id.String()+"/sourceless-cleanup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.SourcelessCleanupDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Exactly ONE entry, and it is 7 — NOT the 5 that comix still carries.
	pages := 5
	num := 7.0
	want := []seriessvc.SourcelessCleanupChapterDTO{{
		ChapterID: removable.ID.String(),
		Number:    &num,
		PageCount: &pages,
		Provider:  "comix",
		Filename:  "7.cbz",
	}}
	if !reflect.DeepEqual(got.Chapters, want) {
		t.Errorf("preview chapters = %+v, want %+v (only 7; 5 = %s must be absent)", got.Chapters, want, protected.ID)
	}
}

// TestSourcelessCleanupPreview_EmptyIsArrayNotNull: a series with nothing to
// clean answers 200 with chapters: [] (never null), so the FE never guards a null.
func TestSourcelessCleanupPreview_EmptyIsArrayNotNull(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/sourceless-cleanup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"chapters":[]`) {
		t.Errorf("body = %s, want chapters marshalled as [] (never null)", rec.Body.String())
	}
}

// TestSourcelessCleanupPreview_NotFound: an unknown series is a 404.
func TestSourcelessCleanupPreview_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/series/"+uuid.New().String()+"/sourceless-cleanup", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestRemoveSourcelessChapters_OK: POST removes the selected chapter's CBZ +
// row and returns {removed: 1}.
func TestRemoveSourcelessChapters_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, removable, _ := seedSourcelessCleanup(ctx, t, env)

	body := `{"chapterIds":["` + removable.ID.String() + `"]}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/sourceless-cleanup", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got handlerResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Removed != 1 {
		t.Errorf("removed = %d, want 1", got.Removed)
	}
	if _, err := os.Stat(filepath.Join(env.storage, "Manga", "Sourceless Saga", "7.cbz")); !os.IsNotExist(err) {
		t.Errorf("7.cbz still on disk (stat err = %v)", err)
	}
	if env.client.Chapter.Query().CountX(ctx) != 1 {
		t.Errorf("chapter rows = %d, want 1 (5 survives)", env.client.Chapter.Query().CountX(ctx))
	}
}

// TestRemoveSourcelessChapters_RejectsNonRemovable: an id the SERVER does not
// consider removable (here 5 — comix still carries it) is a 400 and deletes
// nothing, even though the client asked for it.
func TestRemoveSourcelessChapters_RejectsNonRemovable(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, protected := seedSourcelessCleanup(ctx, t, env)

	body := `{"chapterIds":["` + protected.ID.String() + `"]}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/sourceless-cleanup", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.client.Chapter.Query().CountX(ctx) != 2 {
		t.Error("a chapter row was deleted despite the 400")
	}
	if _, err := os.Stat(filepath.Join(env.storage, "Manga", "Sourceless Saga", "5.cbz")); err != nil {
		t.Errorf("5.cbz was deleted despite the 400: %v", err)
	}
}

// TestRemoveSourcelessChapters_BadBody: an empty or malformed chapterIds list is
// a 400 (a cleanup POST that names nothing is a client bug, not a silent no-op).
func TestRemoveSourcelessChapters_BadBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, _ := seedSourcelessCleanup(ctx, t, env)

	for name, body := range map[string]string{
		"empty list":   `{"chapterIds":[]}`,
		"missing list": `{}`,
		"bad uuid":     `{"chapterIds":["not-a-uuid"]}`,
	} {
		rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/sourceless-cleanup", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// TestRemoveSourcelessChapters_NotFound: an unknown series is a 404.
func TestRemoveSourcelessChapters_NotFound(t *testing.T) {
	env := newTestEnv(t)
	body := `{"chapterIds":["` + uuid.New().String() + `"]}`
	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/sourceless-cleanup", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}
