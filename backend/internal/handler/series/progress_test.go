package series_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

func TestSetProgress_OK(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)

	rec := env.do(http.MethodPatch, "/api/chapters/"+ch.ID.String()+"/progress", `{"lastReadPage":5,"read":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetProgress: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	assertProgressDTO(t, rec, true, 5, true)

	// The GET /api/series/:id ChapterDTO must now round-trip the progress + pageCount.
	rec = env.do(http.MethodGet, "/api/series/"+env.mangaID.String(), "")
	var detail seriessvc.SeriesDetailDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("Detail decode: %v", err)
	}
	cd := findChapterDTO(t, detail.Chapters, "alpha-1")
	if !cd.Read || cd.LastReadPage != 5 {
		t.Fatalf("ChapterDTO progress not round-tripped: %+v", cd)
	}
	if cd.PageCount == nil || *cd.PageCount != 20 {
		t.Fatalf("ChapterDTO pageCount: want 20, got %v", cd.PageCount)
	}
}

// findChapterDTO returns the chapter with the given key from a detail response,
// failing the test if it is absent (keeps the round-trip assertions loop-free).
func findChapterDTO(t *testing.T, chapters []seriessvc.ChapterDTO, key string) seriessvc.ChapterDTO {
	t.Helper()
	for _, cd := range chapters {
		if cd.ChapterKey == key {
			return cd
		}
	}
	t.Fatalf("chapter %q missing from detail", key)
	return seriessvc.ChapterDTO{}
}

func TestSetProgress_UnreadClearsReadAt(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)

	// Mark read, then un-mark: read_at must be cleared on the way back to unread.
	env.do(http.MethodPatch, "/api/chapters/"+ch.ID.String()+"/progress", `{"lastReadPage":9,"read":true}`)
	rec := env.do(http.MethodPatch, "/api/chapters/"+ch.ID.String()+"/progress", `{"lastReadPage":3,"read":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("SetProgress unread: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	assertProgressDTO(t, rec, false, 3, false)
}

// assertProgressDTO decodes a ChapterProgressDTO response and asserts its fields,
// including whether readAt is set. Kept out of the tests so their branch count
// stays under the linter's cyclomatic ceiling.
func assertProgressDTO(t *testing.T, rec *httptest.ResponseRecorder, read bool, lastReadPage int, readAtSet bool) {
	t.Helper()
	var got seriessvc.ChapterProgressDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("progress decode: %v", err)
	}
	if got.Read != read {
		t.Fatalf("progress read: want %v, got %v", read, got.Read)
	}
	if got.LastReadPage != lastReadPage {
		t.Fatalf("progress lastReadPage: want %d, got %d", lastReadPage, got.LastReadPage)
	}
	if readAtSet != (got.ReadAt != nil) {
		t.Fatalf("progress readAt set: want %v, got %+v", readAtSet, got.ReadAt)
	}
}

func TestSetProgress_NegativePage(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)

	rec := env.do(http.MethodPatch, "/api/chapters/"+ch.ID.String()+"/progress", `{"lastReadPage":-1,"read":false}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetProgress negative page: want 400, got %d", rec.Code)
	}
}

func TestSetProgress_MissingField(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	env.seed(ctx, t)
	ch := env.client.Chapter.Query().Where(entchapter.ChapterKey("alpha-1")).OnlyX(ctx)

	// read omitted → 400 (a pointer field must be explicitly present).
	rec := env.do(http.MethodPatch, "/api/chapters/"+ch.ID.String()+"/progress", `{"lastReadPage":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("SetProgress missing read: want 400, got %d", rec.Code)
	}
}

func TestSetProgress_UnknownChapter(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/chapters/"+uuid.New().String()+"/progress", `{"lastReadPage":0,"read":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("SetProgress unknown: want 404, got %d", rec.Code)
	}
}

func TestSetProgress_Unauthenticated(t *testing.T) {
	env := newTestEnv(t)
	rec := env.doUnauth(http.MethodPatch, "/api/chapters/"+uuid.New().String()+"/progress")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("SetProgress unauth: want 401, got %d", rec.Code)
	}
}
