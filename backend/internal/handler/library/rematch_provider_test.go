package library_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestRematchProviderRequiresOwner(t *testing.T) {
	env := newEnv(t)
	req := httptest.NewRequest(http.MethodPost, "/api/series/"+uuid.NewString()+"/providers/"+uuid.NewString()+"/rematch", nil)
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestRematchProviderRejectsInvalidAddress(t *testing.T) {
	env := newEnv(t)
	rec := env.do(http.MethodPost, "/api/series/"+uuid.NewString()+"/providers/"+uuid.NewString()+"/rematch", `{"source":"1","url":"/manga/new","addressMode":"bad"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestRematchProviderRejectsBadProviderID(t *testing.T) {
	env := newEnv(t)
	rec := env.do(http.MethodPost, "/api/series/"+uuid.NewString()+"/providers/nope/rematch", `{"source":"1","url":"/manga/new","addressMode":"direct"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestRematchProviderRoundTrip(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	ctx := context.Background()
	ser := env.client.Series.Create().SetTitle("My Series").SetSlug("my-series-rematch").SaveX(ctx)
	sp := env.client.SeriesProvider.Create().SetSeriesID(ser.ID).SetProvider("1").SetURL(weebMangaURL).SetImportance(17).SaveX(ctx)
	rec := env.do(http.MethodPost, "/api/series/"+ser.ID.String()+"/providers/"+sp.ID.String()+"/rematch", `{"source":"1","url":"/comics/99"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rematch status=%d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != ser.ID.String() {
		t.Fatalf("response id=%v", body["id"])
	}
	if got := env.client.SeriesProvider.GetX(ctx, sp.ID).URL; got != weebRematchURL {
		t.Fatalf("url=%q", got)
	}
}

func TestRematchProviderWithoutSourceRegistryReturns503AndDoesNotMutate(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	ser := env.client.Series.Create().SetTitle("No Registry").SetSlug("no-registry").SaveX(ctx)
	sp := env.client.SeriesProvider.Create().SetSeriesID(ser.ID).SetProvider("1").SetURL("/old").SaveX(ctx)
	rec := env.do(http.MethodPost, "/api/series/"+ser.ID.String()+"/providers/"+sp.ID.String()+"/rematch", `{"source":"1","url":"/new"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
	if got := env.client.SeriesProvider.GetX(ctx, sp.ID).URL; got != "/old" {
		t.Fatalf("url mutated=%q", got)
	}
}

func TestRematchProviderSourceMismatchReturns400AndDoesNotMutate(t *testing.T) {
	assertRematchHTTPFailureNoMutation(t, "Mismatch", "mismatch", `{"source":"2","url":"/new"}`, http.StatusBadRequest)
}

func TestRematchProviderMissingProviderReturns404(t *testing.T) {
	env := newEnvWithMatchIngest(t, t.TempDir())
	rec := env.do(http.MethodPost, "/api/series/"+uuid.NewString()+"/providers/"+uuid.NewString()+"/rematch", `{"source":"1","url":"/comics/99"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d: %s", rec.Code, rec.Body.String())
	}
}

func TestRematchProviderUpstreamFailureReturns502AndDoesNotMutate(t *testing.T) {
	assertRematchHTTPFailureNoMutation(t, "Upstream", "upstream", `{"source":"1","url":"/not-configured"}`, http.StatusBadGateway)
}

func assertRematchHTTPFailureNoMutation(t *testing.T, title, slug, body string, wantStatus int) {
	t.Helper()
	env := newEnvWithMatchIngest(t, t.TempDir())
	ctx := context.Background()
	ser := env.client.Series.Create().SetTitle(title).SetSlug(slug).SaveX(ctx)
	sp := env.client.SeriesProvider.Create().SetSeriesID(ser.ID).SetProvider("1").SetURL("/old").SaveX(ctx)
	rec := env.do(http.MethodPost, "/api/series/"+ser.ID.String()+"/providers/"+sp.ID.String()+"/rematch", body)
	if rec.Code != wantStatus {
		t.Fatalf("status=%d want=%d: %s", rec.Code, wantStatus, rec.Body.String())
	}
	if got := env.client.SeriesProvider.GetX(ctx, sp.ID).URL; got != "/old" {
		t.Fatalf("url mutated=%q", got)
	}
}
