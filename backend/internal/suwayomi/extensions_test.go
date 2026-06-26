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

// fullExtensionNode is a canned ExtensionType node covering every selected field
// with distinctive values so the mapping (and casing) is fully asserted. repo is
// non-null here; the null case is covered by TestClient_Extensions_NullRepo.
func fullExtensionNode() map[string]any {
	return map[string]any{
		"pkgName":     "eu.kanade.tachiyomi.extension.en.mangadex",
		"name":        "MangaDex",
		"lang":        "en",
		"versionName": "1.4.2",
		"versionCode": 42,
		"iconUrl":     "http://suwayomi/icon/mangadex.png",
		"repo":        "https://repo.test/index.min.json",
		"isInstalled": true,
		"hasUpdate":   true,
		"isNsfw":      false,
		"isObsolete":  true,
	}
}

// captureGraphQL decodes the request body's query + variables for assertions and
// writes resp as the response. It centralises the boilerplate every test shares.
func captureGraphQL(t *testing.T, query *string, vars *map[string]any, resp []byte) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if query != nil {
			*query = req.Query
		}
		if vars != nil {
			*vars = req.Variables
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}
}

// TestClient_Extensions_MapsEveryField proves the extensions query result is
// decoded into every Extension field, including the isInstalled/isObsolete casing.
func TestClient_Extensions_MapsEveryField(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"extensions": map[string]any{"nodes": []map[string]any{fullExtensionNode()}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	got, err := newTestClient(t, srv).Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Extensions: got %d, want 1", len(got))
	}
	want := suwayomi.Extension{
		PkgName:     "eu.kanade.tachiyomi.extension.en.mangadex",
		Name:        "MangaDex",
		Lang:        "en",
		VersionName: "1.4.2",
		VersionCode: 42,
		IconURL:     "http://suwayomi/icon/mangadex.png",
		Repo:        "https://repo.test/index.min.json",
		IsInstalled: true,
		HasUpdate:   true,
		IsNsfw:      false,
		IsObsolete:  true,
	}
	if got[0] != want {
		t.Errorf("Extensions mismatch:\n got %+v\nwant %+v", got[0], want)
	}
}

// TestClient_Extensions_NullRepo proves a null repo decodes to "".
func TestClient_Extensions_NullRepo(t *testing.T) {
	node := fullExtensionNode()
	node["repo"] = nil
	resp := graphqlResponse(t, map[string]any{
		"extensions": map[string]any{"nodes": []map[string]any{node}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	got, err := newTestClient(t, srv).Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}
	if got[0].Repo != "" {
		t.Errorf("null repo: got %q, want \"\"", got[0].Repo)
	}
}

// TestClient_Extensions_PropagatesError proves a GraphQL error is surfaced.
func TestClient_Extensions_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "boom"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	if _, err := newTestClient(t, srv).Extensions(context.Background()); err == nil {
		t.Fatal("Extensions: want error, got nil")
	}
}

// TestClient_SetExtensionState_PatchPerAction proves each action sends exactly
// the one matching boolean true in the patch, with id = pkgName.
func TestClient_SetExtensionState_PatchPerAction(t *testing.T) {
	cases := []struct {
		action suwayomi.ExtensionAction
		key    string
	}{
		{suwayomi.ExtensionInstall, "install"},
		{suwayomi.ExtensionUpdate, "update"},
		{suwayomi.ExtensionUninstall, "uninstall"},
	}
	for _, tc := range cases {
		t.Run(string(tc.action), func(t *testing.T) {
			var vars map[string]any
			resp := graphqlResponse(t, map[string]any{
				"updateExtension": map[string]any{"clientMutationId": nil},
			}, nil)
			srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
			defer srv.Close()

			err := newTestClient(t, srv).SetExtensionState(context.Background(), "pkg.test", tc.action)
			if err != nil {
				t.Fatalf("SetExtensionState(%s): %v", tc.action, err)
			}
			if vars["id"] != "pkg.test" {
				t.Errorf("id = %v, want pkg.test", vars["id"])
			}
			patch, ok := vars["patch"].(map[string]any)
			if !ok {
				t.Fatalf("patch missing or wrong type: %v", vars["patch"])
			}
			if len(patch) != 1 {
				t.Fatalf("patch must carry exactly one key, got %v", patch)
			}
			if patch[tc.key] != true {
				t.Errorf("patch[%q] = %v, want true", tc.key, patch[tc.key])
			}
		})
	}
}

// TestClient_SetExtensionState_PropagatesError proves a GraphQL error is surfaced.
func TestClient_SetExtensionState_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "rejected"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	err := newTestClient(t, srv).SetExtensionState(context.Background(), "pkg.test", suwayomi.ExtensionInstall)
	if err == nil {
		t.Fatal("SetExtensionState: want error, got nil")
	}
}

// TestClient_SetExtensionState_UnknownActionGuard proves an unknown action is
// rejected client-side BEFORE any network call: a non-nil error is returned and
// the GraphQL server is never hit (so no empty/garbage patch is ever emitted).
func TestClient_SetExtensionState_UnknownActionGuard(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := newTestClient(t, srv).SetExtensionState(context.Background(), "some.pkg", suwayomi.ExtensionAction("bogus"))
	if err == nil {
		t.Fatal("SetExtensionState(bogus): want error, got nil")
	}
	if hit {
		t.Error("SetExtensionState(bogus): a GraphQL request was sent — the guard must reject before any network call")
	}
}

// TestClient_FetchExtensions_MapsList proves the fetchExtensions mutation result
// decodes into the typed list.
func TestClient_FetchExtensions_MapsList(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"fetchExtensions": map[string]any{"extensions": []map[string]any{fullExtensionNode()}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	got, err := newTestClient(t, srv).FetchExtensions(context.Background())
	if err != nil {
		t.Fatalf("FetchExtensions: %v", err)
	}
	if len(got) != 1 || got[0].PkgName != "eu.kanade.tachiyomi.extension.en.mangadex" {
		t.Errorf("FetchExtensions: unexpected result %+v", got)
	}
}

// TestClient_FetchExtensions_PropagatesError proves a GraphQL error is surfaced.
func TestClient_FetchExtensions_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "boom"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	if _, err := newTestClient(t, srv).FetchExtensions(context.Background()); err == nil {
		t.Fatal("FetchExtensions: want error, got nil")
	}
}

// TestClient_ExtensionRepos_ReadsList proves the settings.extensionRepos query
// result decodes into the string slice.
func TestClient_ExtensionRepos_ReadsList(t *testing.T) {
	resp := graphqlResponse(t, map[string]any{
		"settings": map[string]any{"extensionRepos": []string{"https://a.test/i.json", "https://b.test/i.json"}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	got, err := newTestClient(t, srv).ExtensionRepos(context.Background())
	if err != nil {
		t.Fatalf("ExtensionRepos: %v", err)
	}
	if len(got) != 2 || got[0] != "https://a.test/i.json" || got[1] != "https://b.test/i.json" {
		t.Errorf("ExtensionRepos: unexpected result %v", got)
	}
}

// TestClient_ExtensionRepos_PropagatesError proves a GraphQL error is surfaced.
func TestClient_ExtensionRepos_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "boom"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	if _, err := newTestClient(t, srv).ExtensionRepos(context.Background()); err == nil {
		t.Fatal("ExtensionRepos: want error, got nil")
	}
}

// TestClient_SetExtensionRepos_SendsRepos proves the mutation sends the exact
// repo list under the $repos variable.
func TestClient_SetExtensionRepos_SendsRepos(t *testing.T) {
	var vars map[string]any
	resp := graphqlResponse(t, map[string]any{
		"setSettings": map[string]any{"settings": map[string]any{"extensionRepos": []string{"https://a.test/i.json"}}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
	defer srv.Close()

	err := newTestClient(t, srv).SetExtensionRepos(context.Background(), []string{"https://a.test/i.json"})
	if err != nil {
		t.Fatalf("SetExtensionRepos: %v", err)
	}
	repos, ok := vars["repos"].([]any)
	if !ok {
		t.Fatalf("repos missing or wrong type: %v", vars["repos"])
	}
	if len(repos) != 1 || repos[0] != "https://a.test/i.json" {
		t.Errorf("repos = %v, want [https://a.test/i.json]", repos)
	}
}

// TestClient_SetExtensionRepos_EmptyClears proves an empty slice still sends an
// (empty) repos array — clear-all semantics — without panicking.
func TestClient_SetExtensionRepos_EmptyClears(t *testing.T) {
	var vars map[string]any
	resp := graphqlResponse(t, map[string]any{
		"setSettings": map[string]any{"settings": map[string]any{"extensionRepos": []string{}}},
	}, nil)
	srv := httptest.NewServer(captureGraphQL(t, nil, &vars, resp))
	defer srv.Close()

	if err := newTestClient(t, srv).SetExtensionRepos(context.Background(), []string{}); err != nil {
		t.Fatalf("SetExtensionRepos empty: %v", err)
	}
	repos, ok := vars["repos"].([]any)
	if !ok || len(repos) != 0 {
		t.Errorf("empty clear: repos = %v, want empty array", vars["repos"])
	}
}

// TestClient_SetExtensionRepos_PropagatesError proves a GraphQL error is surfaced.
func TestClient_SetExtensionRepos_PropagatesError(t *testing.T) {
	resp := graphqlResponse(t, nil, []map[string]any{{"message": "rejected"}})
	srv := httptest.NewServer(captureGraphQL(t, nil, nil, resp))
	defer srv.Close()

	err := newTestClient(t, srv).SetExtensionRepos(context.Background(), []string{"https://a.test/i.json"})
	if err == nil {
		t.Fatal("SetExtensionRepos: want error, got nil")
	}
}
