package suwayomi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// fullSettingsNode is a canned settings response covering every proxied field
// with distinctive non-zero values so the mapping is fully asserted.
func fullSettingsNode() map[string]any {
	return map[string]any{
		"flareSolverrEnabled":            true,
		"flareSolverrUrl":                "http://flare:8191",
		"flareSolverrTimeout":            60,
		"flareSolverrSessionName":        "sess",
		"flareSolverrSessionTtl":         15,
		"flareSolverrAsResponseFallback": true,
		"socksProxyEnabled":              true,
		"socksProxyVersion":              5,
		"socksProxyHost":                 "127.0.0.1",
		"socksProxyPort":                 "1080",
		"socksProxyUsername":             "user",
		"socksProxyPassword":             "pass",
	}
}

// TestClient_ServerSettings_MapsEveryField proves the settings query result is
// decoded into every field of SuwayomiSettings.
func TestClient_ServerSettings_MapsEveryField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, map[string]any{"settings": fullSettingsNode()}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	got, err := newTestClient(t, srv).ServerSettings(context.Background())
	if err != nil {
		t.Fatalf("ServerSettings: %v", err)
	}

	want := suwayomi.SuwayomiSettings{
		FlareSolverrEnabled:            true,
		FlareSolverrURL:                "http://flare:8191",
		FlareSolverrTimeout:            60,
		FlareSolverrSessionName:        "sess",
		FlareSolverrSessionTTL:         15,
		FlareSolverrAsResponseFallback: true,
		SocksProxyEnabled:              true,
		SocksProxyVersion:              5,
		SocksProxyHost:                 "127.0.0.1",
		SocksProxyPort:                 "1080",
		SocksProxyUsername:             "user",
		SocksProxyPassword:             "pass",
	}
	if got != want {
		t.Errorf("ServerSettings mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestClient_ServerSettings_PropagatesError proves a GraphQL application error is
// surfaced, never silently swallowed.
func TestClient_ServerSettings_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{{"message": "boom"}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	if _, err := newTestClient(t, srv).ServerSettings(context.Background()); err == nil {
		t.Fatal("ServerSettings: want error, got nil")
	}
}

// TestClient_SetServerSettings_SendsOnlyProvidedFields is the no-clobber proof:
// the outgoing PartialSettingsTypeInput must contain ONLY the fields the patch
// set — every other (unset) field is absent so Suwayomi leaves it untouched.
func TestClient_SetServerSettings_SendsOnlyProvidedFields(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		settings, _ := req.Variables["settings"].(map[string]any)
		captured = settings
		resp := graphqlResponse(t, map[string]any{"setSettings": map[string]any{"settings": fullSettingsNode()}}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	enabled := true
	port := "9050"
	patch := suwayomi.SuwayomiSettingsPatch{
		FlareSolverrEnabled: &enabled,
		SocksProxyPort:      &port,
	}
	if err := newTestClient(t, srv).SetServerSettings(context.Background(), patch); err != nil {
		t.Fatalf("SetServerSettings: %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("expected exactly 2 keys in partial input, got %d: %v", len(captured), captured)
	}
	if captured["flareSolverrEnabled"] != true {
		t.Errorf("flareSolverrEnabled = %v, want true", captured["flareSolverrEnabled"])
	}
	if captured["socksProxyPort"] != "9050" {
		t.Errorf("socksProxyPort = %v, want 9050", captured["socksProxyPort"])
	}
	// No other proxied key may leak into the partial input.
	for _, k := range []string{"flareSolverrUrl", "socksProxyEnabled", "socksProxyHost", "socksProxyVersion"} {
		if _, ok := captured[k]; ok {
			t.Errorf("unset field %q leaked into partial input (would clobber)", k)
		}
	}
}

// TestClient_SetServerSettings_PropagatesError proves a GraphQL error from the
// mutation is surfaced.
func TestClient_SetServerSettings_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := graphqlResponse(t, nil, []map[string]any{{"message": "rejected"}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	enabled := false
	patch := suwayomi.SuwayomiSettingsPatch{SocksProxyEnabled: &enabled}
	if err := newTestClient(t, srv).SetServerSettings(context.Background(), patch); err == nil {
		t.Fatal("SetServerSettings: want error, got nil")
	}
}

// TestSettingsPatch_EmptyYieldsEmptyObject documents that an empty patch sends an
// empty settings object (a no-op) — the handler's validation prevents this from
// ever being reached in production, but the client must not panic on it.
func TestSettingsPatch_EmptyYieldsEmptyObject(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		captured, _ = req.Variables["settings"].(map[string]any)
		resp := graphqlResponse(t, map[string]any{"setSettings": map[string]any{"settings": fullSettingsNode()}}, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	if err := newTestClient(t, srv).SetServerSettings(context.Background(), suwayomi.SuwayomiSettingsPatch{}); err != nil {
		t.Fatalf("SetServerSettings empty: %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("empty patch should send no fields, got %v", captured)
	}
}
