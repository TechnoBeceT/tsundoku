package suwayomi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClient_Sources_MetaAbsentDefaultsEnabled proves a source with no
// isEnabled meta key at all decodes to Disabled=false (enabled) — Suwayomi's
// own default (per the client convention: absent means enabled).
func TestClient_Sources_MetaAbsentDefaultsEnabled(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"sources": map[string]any{
			"nodes": []map[string]any{
				{"id": "1", "name": "MangaDex", "lang": "en", "meta": []map[string]any{}},
			},
		},
	}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	sources, err := newTestClient(t, srv).Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources() error = %v", err)
	}
	if len(sources) != 1 || sources[0].Disabled {
		t.Fatalf("Sources() = %+v, want one enabled (Disabled=false) source", sources)
	}
}

// TestClient_Sources_MetaFalseDisables proves isEnabled="false" decodes to
// Disabled=true.
func TestClient_Sources_MetaFalseDisables(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"sources": map[string]any{
			"nodes": []map[string]any{
				{"id": "1", "name": "MangaDex", "lang": "en", "meta": []map[string]any{
					{"key": "isEnabled", "value": "false"},
				}},
			},
		},
	}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	sources, err := newTestClient(t, srv).Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources() error = %v", err)
	}
	if len(sources) != 1 || !sources[0].Disabled {
		t.Fatalf("Sources() = %+v, want one disabled (Disabled=true) source", sources)
	}
}

// TestClient_Sources_MetaTrueEnables proves isEnabled="true" (an explicit
// re-enable write, not just the absent default) decodes to Disabled=false.
func TestClient_Sources_MetaTrueEnables(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"sources": map[string]any{
			"nodes": []map[string]any{
				{"id": "1", "name": "MangaDex", "lang": "en", "meta": []map[string]any{
					{"key": "isEnabled", "value": "true"},
					{"key": "someOtherKey", "value": "irrelevant"},
				}},
			},
		},
	}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	sources, err := newTestClient(t, srv).Sources(context.Background())
	if err != nil {
		t.Fatalf("Sources() error = %v", err)
	}
	if len(sources) != 1 || sources[0].Disabled {
		t.Fatalf("Sources() = %+v, want one enabled (Disabled=false) source", sources)
	}
}

// TestClient_ExtensionSources_MapsMeta proves the extension→sources selection
// also decodes meta into Disabled — the same SourceType field via a different
// root query (drives the Configure dialog's per-language toggle).
func TestClient_ExtensionSources_MapsMeta(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"extension": map[string]any{
			"source": map[string]any{
				"nodes": []map[string]any{
					{"id": "1", "name": "Comick EN", "lang": "en", "meta": []map[string]any{}},
					{"id": "2", "name": "Comick RU", "lang": "ru", "meta": []map[string]any{
						{"key": "isEnabled", "value": "false"},
					}},
				},
			},
		},
	}, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
	defer srv.Close()

	sources, err := newTestClient(t, srv).ExtensionSources(context.Background(), "pkg.test")
	if err != nil {
		t.Fatalf("ExtensionSources() error = %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("ExtensionSources() got %d sources, want 2", len(sources))
	}
	if sources[0].Disabled {
		t.Errorf("sources[0] (EN, no meta) Disabled = true, want false")
	}
	if !sources[1].Disabled {
		t.Errorf("sources[1] (RU, isEnabled=false) Disabled = false, want true")
	}
}

// TestClient_SetSourceEnabled_SendsCorrectMutation proves SetSourceEnabled
// sends the documented setSourceMeta input for both the disable and the
// EXPLICIT re-enable write (the owner-ratified design never deletes the meta
// row — re-enable must send the literal string "true", not omit the field).
func TestClient_SetSourceEnabled_SendsCorrectMutation(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		want    string
	}{
		{"disable", false, "false"},
		{"reEnable", true, "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var vars map[string]any
			resp := graphqlResponse(t, map[string]any{
				"setSourceMeta": map[string]any{"clientMutationId": nil},
			}, nil)
			srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
			defer srv.Close()

			err := newTestClient(t, srv).SetSourceEnabled(context.Background(), "42", tc.enabled)
			if err != nil {
				t.Fatalf("SetSourceEnabled(%v): %v", tc.enabled, err)
			}
			input, ok := vars["input"].(map[string]any)
			if !ok {
				t.Fatalf("input missing or wrong type: %v", vars["input"])
			}
			meta, ok := input["meta"].(map[string]any)
			if !ok {
				t.Fatalf("input.meta missing or wrong type: %v", input["meta"])
			}
			if meta["sourceId"] != "42" {
				t.Errorf("meta.sourceId = %v, want 42", meta["sourceId"])
			}
			if meta["key"] != "isEnabled" {
				t.Errorf("meta.key = %v, want isEnabled", meta["key"])
			}
			if meta["value"] != tc.want {
				t.Errorf("meta.value = %v, want %q", meta["value"], tc.want)
			}
		})
	}
}

// TestClient_SetSourceEnabled_PropagatesError proves a GraphQL error is surfaced.
func TestClient_SetSourceEnabled_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "rejected"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	err := newTestClient(t, srv).SetSourceEnabled(context.Background(), "42", false)
	if err == nil {
		t.Fatal("SetSourceEnabled: want error, got nil")
	}
}
