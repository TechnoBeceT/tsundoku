// Package category_test exercises the category HTTP handlers end-to-end through a
// real Echo instance (with RequireOwner wired) against an ephemeral PostgreSQL
// instance (testdb). Tests require Docker.
package category_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	categorysvc "github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	handler "github.com/technobecet/tsundoku/internal/handler/category"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

const testSecret = "category-handler-test-secret"

type testEnv struct {
	e      *echo.Echo
	client *ent.Client
	token  string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	client := testdb.New(t)
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(categorysvc.NewService(client, t.TempDir()))

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/categories", h.List)
	authed.POST("/categories", h.Create)
	authed.PATCH("/categories/:id", h.Update)
	authed.DELETE("/categories/:id", h.Delete)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &testEnv{e: e, client: client, token: token}
}

func (env *testEnv) do(method, target, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+env.token)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

func (env *testEnv) catID(ctx context.Context, t *testing.T, name string) uuid.UUID {
	t.Helper()
	id, err := categorysvc.IDByName(ctx, env.client, name)
	if err != nil {
		t.Fatalf("IDByName(%q): %v", name, err)
	}
	return id
}

// TestList_OK verifies GET /api/categories returns the five seeded defaults.
func TestList_OK(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodGet, "/api/categories", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("List: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got []categorysvc.CategoryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("List: want 5, got %d", len(got))
	}
}

// TestCreate_OK verifies POST creates a category and returns 201 with the DTO.
func TestCreate_OK(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(http.MethodPost, "/api/categories", `{"name":"Indie","sortOrder":9}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("Create: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got categorysvc.CategoryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Indie" || got.SortOrder != 9 || got.Count != 0 {
		t.Fatalf("Create dto: %+v", got)
	}
}

// TestCreate_Duplicate verifies a duplicate name yields 409.
func TestCreate_Duplicate(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/categories", `{"name":"Manga"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("Create dup: want 409, got %d", rec.Code)
	}
}

// TestCreate_InvalidName verifies a filesystem-unsafe name yields 400.
func TestCreate_InvalidName(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPost, "/api/categories", `{"name":"a/b"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Create invalid: want 400, got %d", rec.Code)
	}
}

// TestUpdate_Rename verifies PATCH renames and returns the updated DTO.
func TestUpdate_Rename(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id := env.catID(ctx, t, "Comic")

	rec := env.do(http.MethodPatch, "/api/categories/"+id.String(), `{"name":"Western Comics"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update rename: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got categorysvc.CategoryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "Western Comics" {
		t.Fatalf("Update rename: name = %q, want Western Comics", got.Name)
	}
}

// TestUpdate_Reorder verifies a DB-only sortOrder update is reflected.
func TestUpdate_Reorder(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id := env.catID(ctx, t, "Manhua")

	rec := env.do(http.MethodPatch, "/api/categories/"+id.String(), `{"sortOrder":42}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update reorder: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got categorysvc.CategoryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.SortOrder != 42 {
		t.Fatalf("Update reorder: sortOrder = %d, want 42", got.SortOrder)
	}
}

// TestUpdate_EmptyBody verifies a PATCH with neither field yields 400.
func TestUpdate_EmptyBody(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id := env.catID(ctx, t, "Manga")
	rec := env.do(http.MethodPatch, "/api/categories/"+id.String(), `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update empty: want 400, got %d", rec.Code)
	}
}

// TestUpdate_Protected verifies renaming the protected default yields 400.
func TestUpdate_Protected(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id := env.catID(ctx, t, "Other")
	rec := env.do(http.MethodPatch, "/api/categories/"+id.String(), `{"name":"Misc"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Update protected: want 400, got %d", rec.Code)
	}
}

// TestUpdate_NotFound verifies a PATCH on an unknown id yields 404.
func TestUpdate_NotFound(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodPatch, "/api/categories/"+uuid.New().String(), `{"sortOrder":1}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("Update unknown: want 404, got %d", rec.Code)
	}
}

// TestDelete_OK verifies an empty category is deletable (204).
func TestDelete_OK(t *testing.T) {
	env := newTestEnv(t)
	// Create then delete.
	rec := env.do(http.MethodPost, "/api/categories", `{"name":"Temp"}`)
	var created categorysvc.CategoryDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rec = env.do(http.MethodDelete, "/api/categories/"+created.ID, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("Delete: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestDelete_NonEmpty verifies a category with series yields 409.
func TestDelete_NonEmpty(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id := env.catID(ctx, t, "Manga")
	env.client.Series.Create().SetTitle("Z").SetSlug("z").SetCategoryID(id).SaveX(ctx)
	rec := env.do(http.MethodDelete, "/api/categories/"+id.String(), "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("Delete non-empty: want 409, got %d", rec.Code)
	}
}

// TestDelete_Protected verifies the protected default yields 400.
func TestDelete_Protected(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	id := env.catID(ctx, t, "Other")
	rec := env.do(http.MethodDelete, "/api/categories/"+id.String(), "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Delete protected: want 400, got %d", rec.Code)
	}
}

// TestDelete_BadID verifies a malformed id yields 400.
func TestDelete_BadID(t *testing.T) {
	env := newTestEnv(t)
	rec := env.do(http.MethodDelete, "/api/categories/not-a-uuid", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("Delete bad id: want 400, got %d", rec.Code)
	}
}

// TestAuthz_AllRoutesReject401 proves every category route requires auth.
func TestAuthz_AllRoutesReject401(t *testing.T) {
	env := newTestEnv(t)
	id := uuid.New().String()
	cases := []struct{ method, target string }{
		{http.MethodGet, "/api/categories"},
		{http.MethodPost, "/api/categories"},
		{http.MethodPatch, "/api/categories/" + id},
		{http.MethodDelete, "/api/categories/" + id},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.target, nil)
		rec := httptest.NewRecorder()
		env.e.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: want 401, got %d", tc.method, tc.target, rec.Code)
		}
	}
}
