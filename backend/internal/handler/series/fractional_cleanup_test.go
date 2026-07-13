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

// seedCleanup builds one series with an IGNORED re-uploader (kaliscan) and a LIVE
// source (comix), plus the chapters the removable rule has to tell apart:
//
//	5     — whole, downloaded          → never removable
//	5.1   — fractional, ignored-only   → REMOVABLE (a real CBZ is written for it)
//	6.1   — fractional, ALSO carried by the live comix → never removable
//
// It returns the series id and the two fractional chapters.
func seedCleanup(ctx context.Context, t *testing.T, env *testEnv) (seriesID uuid.UUID, removable, protected *ent.Chapter) {
	t.Helper()
	client := env.client

	s := client.Series.Create().
		SetTitle("Cleanup Saga").SetSlug("cleanup-saga").
		SetCategoryID(catID(ctx, client, "Manga")).SaveX(ctx)

	ignored := client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("kaliscan").SetImportance(40).
		SetIgnoreFractional(true).SaveX(ctx)
	live := client.SeriesProvider.Create().
		SetSeriesID(s.ID).SetProvider("comix").SetImportance(60).SaveX(ctx)

	feed := func(sp *ent.SeriesProvider, key string, n float64) {
		client.ProviderChapter.Create().
			SetSeriesProviderID(sp.ID).SetChapterKey(key).SetNumber(n).SaveX(ctx)
	}
	feed(live, "5", 5)
	feed(live, "6.1", 6.1) // the live source also carries 6.1 → resurrection guard
	feed(ignored, "5.1", 5.1)
	feed(ignored, "6.1", 6.1)

	dir := filepath.Join(env.storage, "Manga", "Cleanup Saga")
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
	downloaded("5", 5, 90, live)
	removable = downloaded("5.1", 5.1, 2, ignored)
	protected = downloaded("6.1", 6.1, 3, ignored)
	return s.ID, removable, protected
}

// TestFractionalCleanupPreview_OK: GET returns the removable set with the evidence
// (page count, satisfying source, filename) and the median whole-chapter yardstick,
// and it excludes the fractional a live source still carries.
func TestFractionalCleanupPreview_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, removable, protected := seedCleanup(ctx, t, env)

	rec := env.do(http.MethodGet, "/api/series/"+id.String()+"/fractional-cleanup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	var got seriessvc.FractionalCleanupDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Exactly ONE entry, and it is 5.1 — NOT the 6.1 that the live comix still
	// carries (the resurrection guard, proven through the HTTP layer).
	pages := 2
	want := []seriessvc.FractionalCleanupChapterDTO{{
		ChapterID: removable.ID.String(),
		Number:    5.1,
		PageCount: &pages,
		Provider:  "kaliscan",
		Filename:  "5.1.cbz",
	}}
	if !reflect.DeepEqual(got.Chapters, want) {
		t.Errorf("preview chapters = %+v, want %+v (only 5.1; 6.1 = %s must be absent)", got.Chapters, want, protected.ID)
	}
	if got.TypicalPageCount != 90 {
		t.Errorf("typicalPageCount = %d, want 90 (the only whole downloaded chapter)", got.TypicalPageCount)
	}
}

// TestFractionalCleanupPreview_EmptyIsArrayNotNull: a series with nothing to clean
// answers 200 with chapters: [] (never null), so the FE never guards a null.
func TestFractionalCleanupPreview_EmptyIsArrayNotNull(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)

	rec := env.do(http.MethodGet, "/api/series/"+env.mangaID.String()+"/fractional-cleanup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"chapters":[]`) {
		t.Errorf("body = %s, want chapters marshalled as [] (never null)", rec.Body.String())
	}
}

// TestFractionalCleanupPreview_NotFound: an unknown series is a 404.
func TestFractionalCleanupPreview_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodGet, "/api/series/"+uuid.New().String()+"/fractional-cleanup", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestRemoveFractionalChapters_OK: POST removes the selected chapter's CBZ + row,
// returns {removed: 1}, and KEEPS the provider feed rows (un-ticking the toggle must
// restore the chapter).
func TestRemoveFractionalChapters_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, removable, _ := seedCleanup(ctx, t, env)
	feedBefore := env.client.ProviderChapter.Query().CountX(ctx)

	body := `{"chapterIds":["` + removable.ID.String() + `"]}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/fractional-cleanup", body)
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
	if _, err := os.Stat(filepath.Join(env.storage, "Manga", "Cleanup Saga", "5.1.cbz")); !os.IsNotExist(err) {
		t.Errorf("5.1.cbz still on disk (stat err = %v)", err)
	}
	if env.client.Chapter.Query().CountX(ctx) != 2 {
		t.Errorf("chapter rows = %d, want 2 (5 and 6.1 survive)", env.client.Chapter.Query().CountX(ctx))
	}
	if after := env.client.ProviderChapter.Query().CountX(ctx); after != feedBefore {
		t.Errorf("ProviderChapter rows %d → %d — the feed must survive a removal", feedBefore, after)
	}
}

// handlerResult mirrors the {removed:N} response body.
type handlerResult struct {
	Removed int `json:"removed"`
}

// TestRemoveFractionalChapters_RejectsNonRemovable: an id the SERVER does not
// consider removable (here 6.1 — a live source still carries it) is a 400 and
// deletes nothing, even though the client asked for it. The client's list is a
// selection, never an authorisation.
func TestRemoveFractionalChapters_RejectsNonRemovable(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, protected := seedCleanup(ctx, t, env)

	body := `{"chapterIds":["` + protected.ID.String() + `"]}`
	rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/fractional-cleanup", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if env.client.Chapter.Query().CountX(ctx) != 3 {
		t.Error("a chapter row was deleted despite the 400")
	}
	if _, err := os.Stat(filepath.Join(env.storage, "Manga", "Cleanup Saga", "6.1.cbz")); err != nil {
		t.Errorf("6.1.cbz was deleted despite the 400: %v", err)
	}
}

// TestRemoveFractionalChapters_BadBody: an empty or malformed chapterIds list is a
// 400 (a cleanup POST that names nothing is a client bug, not a silent no-op).
func TestRemoveFractionalChapters_BadBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id, _, _ := seedCleanup(ctx, t, env)

	for name, body := range map[string]string{
		"empty list":   `{"chapterIds":[]}`,
		"missing list": `{}`,
		"bad uuid":     `{"chapterIds":["not-a-uuid"]}`,
	} {
		rec := env.do(http.MethodPost, "/api/series/"+id.String()+"/fractional-cleanup", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d (%s)", name, rec.Code, rec.Body.String())
		}
	}
}

// TestRemoveFractionalChapters_NotFound: an unknown series is a 404.
func TestRemoveFractionalChapters_NotFound(t *testing.T) {
	env := newTestEnv(t)
	body := `{"chapterIds":["` + uuid.New().String() + `"]}`
	rec := env.do(http.MethodPost, "/api/series/"+uuid.New().String()+"/fractional-cleanup", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", rec.Code, rec.Body.String())
	}
}
