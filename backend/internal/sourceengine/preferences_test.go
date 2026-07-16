package sourceengine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// preferencesResponseBody is the canned {preferences:[...]} body both the GET
// and PUT preferences tests write back.
func preferencesResponseBody() map[string]any {
	return map[string]any{
		"preferences": []map[string]any{
			{
				"key": "useSourceLang", "type": "SwitchPreferenceCompat", "title": "Use source language",
				"summary": "", "currentValue": true, "defaultValue": false,
				"entries": nil, "entryValues": nil,
			},
		},
	}
}

func wantPreferences() []sourceengine.Preference {
	return []sourceengine.Preference{
		{
			Key: "useSourceLang", Type: "SwitchPreferenceCompat", Title: "Use source language",
			Summary: "", CurrentValue: true, DefaultValue: false,
		},
	}
}

// TestPreferences_Success proves GET /sources/{id}/preferences unwraps the
// {preferences:[...]} response into []Preference.
func TestPreferences_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sources/7/preferences" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, preferencesResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).Preferences(context.Background(), 7)
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	if !reflect.DeepEqual(got, wantPreferences()) {
		t.Errorf("Preferences = %+v, want %+v", got, wantPreferences())
	}
}

// TestSetPreferences_Success proves PUT /sources/{id}/preferences sends the
// raw {key:value} map body and unwraps the refreshed {preferences:[...]}.
func TestSetPreferences_Success(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/sources/7/preferences" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		decodeBody(t, r, &captured)
		writeJSON(t, w, http.StatusOK, preferencesResponseBody())
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).SetPreferences(context.Background(), 7, map[string]any{"useSourceLang": true})
	if err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}
	if !reflect.DeepEqual(got, wantPreferences()) {
		t.Errorf("SetPreferences = %+v, want %+v", got, wantPreferences())
	}
	if captured["useSourceLang"] != true {
		t.Errorf("request body = %+v, want useSourceLang=true", captured)
	}
}

// TestPreferences_BadRequest proves a 400 maps to *BadRequestError.
func TestPreferences_BadRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]string{"error": "invalid sourceId in path"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).Preferences(context.Background(), 7)
	assertBadRequestError(t, err)
}

// TestSetPreferences_UpstreamFailure proves a 502 maps to *UpstreamError.
func TestSetPreferences_UpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{"error": "boom"})
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv).SetPreferences(context.Background(), 7, map[string]any{"x": 1})
	assertUpstreamError(t, err, http.StatusBadGateway)
}
