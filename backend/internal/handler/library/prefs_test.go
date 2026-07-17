package library_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/technobecet/tsundoku/internal/library"
)

func TestGetPrefs_DefaultsWhenUnset(t *testing.T) {
	env := newEnv(t)

	rec := env.do(http.MethodGet, "/api/library/prefs", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET prefs: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got library.LibraryPrefs
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SortKey != "title" || got.SortDir != "asc" {
		t.Fatalf("default prefs: want title/asc, got %s/%s", got.SortKey, got.SortDir)
	}
	if got.Filters.Downloaded || got.Filters.Unread || got.Filters.Completed || got.Filters.NeedsSource {
		t.Fatalf("default filters: want all false, got %+v", got.Filters)
	}
}

func TestPutPrefs_RoundTrips(t *testing.T) {
	env := newEnv(t)

	body := `{"sortKey":"unread","sortDir":"desc","filters":{"downloaded":true,"unread":false,"completed":true,"needsSource":false}}`
	rec := env.do(http.MethodPut, "/api/library/prefs", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT prefs: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}

	// GET must reflect the stored value (§16 round-trip through the DB).
	rec = env.do(http.MethodGet, "/api/library/prefs", "")
	var got library.LibraryPrefs
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SortKey != "unread" || got.SortDir != "desc" {
		t.Fatalf("stored prefs: want unread/desc, got %s/%s", got.SortKey, got.SortDir)
	}
	if !got.Filters.Downloaded || !got.Filters.Completed || got.Filters.Unread || got.Filters.NeedsSource {
		t.Fatalf("stored filters: got %+v", got.Filters)
	}
}

func TestPutPrefs_RejectsUnknownSortKey(t *testing.T) {
	env := newEnv(t)

	rec := env.do(http.MethodPut, "/api/library/prefs", `{"sortKey":"bogus","sortDir":"asc","filters":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad sortKey: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestPutPrefs_RejectsBadDirection(t *testing.T) {
	env := newEnv(t)

	rec := env.do(http.MethodPut, "/api/library/prefs", `{"sortKey":"title","sortDir":"sideways","filters":{}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad dir: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestPrefs_Unauthorized(t *testing.T) {
	env := newEnv(t)

	if rec := env.doUnauth(http.MethodGet, "/api/library/prefs", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET prefs unauth: want 401, got %d", rec.Code)
	}
	if rec := env.doUnauth(http.MethodPut, "/api/library/prefs", `{"sortKey":"title","sortDir":"asc","filters":{}}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("PUT prefs unauth: want 401, got %d", rec.Code)
	}
}
