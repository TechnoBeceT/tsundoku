package library_test

import (
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
