package kitsu_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/kitsu"
)

// redirectTransport rewrites every outgoing request's scheme+host to
// target's — needed because kitsu.Client posts to hardcoded endpoint
// constants (apiBaseURL/tokenURL); mirrors
// internal/tracker/mal/client_test.go's identical helper.
type redirectTransport struct {
	target *url.URL
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func newTestClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return &http.Client{Transport: &redirectTransport{target: u}}
}

// TestClient_IdentityGetters pins the fixed Key/ID/Name/NeedsOAuth this
// Client reports in the tracker.Tracker contract.
func TestClient_IdentityGetters(t *testing.T) {
	c := kitsu.New(nil)
	if c.Key() != "kitsu" {
		t.Fatalf("Key() = %q, want kitsu", c.Key())
	}
	if c.ID() != tracker.IDKitsu {
		t.Fatalf("ID() = %d, want tracker.IDKitsu (%d)", c.ID(), tracker.IDKitsu)
	}
	if c.Name() != "Kitsu" {
		t.Fatalf("Name() = %q, want Kitsu", c.Name())
	}
	if c.NeedsOAuth() {
		t.Fatalf("NeedsOAuth() = true, want false — Kitsu connects via LoginCredentials")
	}
}

// TestAuthURL_ReturnsErrOAuthNotSupported confirms Kitsu (a credential-login
// tracker) fails closed on the OAuth-redirect surface.
func TestAuthURL_ReturnsErrOAuthNotSupported(t *testing.T) {
	c := kitsu.New(nil)
	if _, _, err := c.AuthURL("state", "https://example.test/callback"); !errors.Is(err, tracker.ErrOAuthNotSupported) {
		t.Fatalf("AuthURL: err = %v, want tracker.ErrOAuthNotSupported", err)
	}
	if _, err := c.ExchangeCode(context.Background(), "code", "verifier", "https://example.test/cb"); !errors.Is(err, tracker.ErrOAuthNotSupported) {
		t.Fatalf("ExchangeCode: err = %v, want tracker.ErrOAuthNotSupported", err)
	}
}

// TestLoginCredentials_RequestBodyShape pins the OAuth2 password-grant form
// body LoginCredentials POSTs: grant_type=password, the given
// username/password verbatim, plus Kitsu's public native client_id/secret.
func TestLoginCredentials_RequestBodyShape(t *testing.T) {
	var gotForm url.Values
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"kitsu-access","refresh_token":"kitsu-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	tok, err := c.LoginCredentials(context.Background(), "owner@example.test", "hunter2")
	if err != nil {
		t.Fatalf("LoginCredentials: %v", err)
	}

	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	assertFormField(t, gotForm, "grant_type", "password")
	assertFormField(t, gotForm, "username", "owner@example.test")
	assertFormField(t, gotForm, "password", "hunter2")
	if gotForm.Get("client_id") == "" {
		t.Fatalf("form carried no client_id — Kitsu's password grant requires one")
	}

	if tok.Access != "kitsu-access" || tok.Refresh != "kitsu-refresh" {
		t.Fatalf("TokenSet = %+v", tok)
	}
	if tok.ExpiresAt == nil {
		t.Fatalf("TokenSet.ExpiresAt = nil, want a computed expiry")
	}
}

func assertFormField(t *testing.T, v url.Values, key, want string) {
	t.Helper()
	if got := v.Get(key); got != want {
		t.Fatalf("form[%s] = %q, want %q", key, got, want)
	}
}

// TestRefresh_EmptyTokenIsErrNoRefresh confirms Refresh never issues a
// network call for an empty refresh token.
func TestRefresh_EmptyTokenIsErrNoRefresh(t *testing.T) {
	c := kitsu.New(nil)
	if _, err := c.Refresh(context.Background(), ""); !errors.Is(err, tracker.ErrNoRefresh) {
		t.Fatalf("Refresh(\"\"): err = %v, want tracker.ErrNoRefresh", err)
	}
}

// TestRefresh_RequestBodyShape pins the refresh_token grant's form body.
func TestRefresh_RequestBodyShape(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed","refresh_token":"refreshed-r","expires_in":3600}`))
	}))
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	tok, err := c.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	assertFormField(t, gotForm, "grant_type", "refresh_token")
	assertFormField(t, gotForm, "refresh_token", "old-refresh")
	if tok.Access != "refreshed" {
		t.Fatalf("TokenSet.Access = %q, want refreshed", tok.Access)
	}
}

// TestClient_Search_Parses drives Search against a fake JSON:API server.
func TestClient_Search_Parses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/edge/manga" {
			t.Errorf("Search path = %q, want /api/edge/manga", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/vnd.api+json")
		_, _ = w.Write([]byte(`{"data":[{"id":"7224","attributes":{"slug":"solo-leveling","canonicalTitle":"Solo Leveling","status":"finished","chapterCount":179,"posterImage":{"original":"https://x/y.jpg"}}}]}`))
	}))
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	results, err := c.Search(context.Background(), "", "solo leveling")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Search returned %d results, want 1", len(results))
	}
	got := results[0]
	if got.RemoteID != "7224" || got.Title != "Solo Leveling" || got.TotalChapters != 179 {
		t.Fatalf("Search result = %+v", got)
	}
	if got.URL != "https://kitsu.app/manga/solo-leveling" {
		t.Fatalf("Search result URL = %q", got.URL)
	}
}

// kitsuAPIServer builds a fake Kitsu edge server multiplexing the self-user
// lookup and the library-entries surface — shared by the GetEntry/Save/
// Update/Delete tests below, which all first resolve the caller's own user
// id (see Client.selfUserID) before touching a library-entry.
func kitsuAPIServer(t *testing.T, handleLibraryEntries http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/edge/users", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("filter[self]"); got != "true" {
			t.Errorf("self lookup filter[self] = %q, want true", got)
		}
		w.Header().Set("Content-Type", "application/vnd.api+json")
		_, _ = w.Write([]byte(`{"data":[{"id":"555"}]}`))
	})
	mux.HandleFunc("/api/edge/library-entries", handleLibraryEntries)
	mux.HandleFunc("/api/edge/library-entries/", handleLibraryEntries)
	return httptest.NewServer(mux)
}

// TestClient_GetEntry_NotYetTracked confirms an empty JSON:API data array
// maps to (nil, nil) — Kitsu's "not on this account's library" shape.
func TestClient_GetEntry_NotYetTracked(t *testing.T) {
	srv := kitsuAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("filter[mangaId]"); got != "7224" {
			t.Errorf("filter[mangaId] = %q, want 7224", got)
		}
		w.Header().Set("Content-Type", "application/vnd.api+json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	entry, err := c.GetEntry(context.Background(), "acct-token", "7224")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry != nil {
		t.Fatalf("GetEntry = %+v, want nil (not yet tracked)", entry)
	}
}

// TestClient_GetEntry_Found parses a real library-entry hit.
func TestClient_GetEntry_Found(t *testing.T) {
	srv := kitsuAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.api+json")
		_, _ = w.Write([]byte(`{"data":[{"id":"999","attributes":{"status":"current","progress":42,"ratingTwenty":16,"private":false},"relationships":{"manga":{"data":{"id":"7224","type":"manga"}}}}]}`))
	})
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	entry, err := c.GetEntry(context.Background(), "acct-token", "7224")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry == nil {
		t.Fatal("GetEntry = nil, want a populated entry")
	}
	if entry.RemoteID != "7224" || entry.LibraryID != "999" || entry.Status != "current" || entry.Progress != 42 || entry.Score != 16 {
		t.Fatalf("GetEntry = %+v", entry)
	}
}

// TestClient_SaveEntry_RequestBodyShape pins the POST /library-entries
// JSON:API create body: type "library-entries", attributes carrying
// status/progress, and relationships naming BOTH the user (resolved via
// self-lookup) and the media (entry.RemoteID) explicitly.
func TestClient_SaveEntry_RequestBodyShape(t *testing.T) {
	var gotBody map[string]any
	var gotMethod, gotContentType string
	srv := kitsuAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/vnd.api+json")
		_, _ = w.Write([]byte(`{"data":{"id":"999","attributes":{"status":"current","progress":5,"ratingTwenty":null,"private":false},"relationships":{"manga":{"data":{"id":"7224","type":"manga"}}}}}`))
	})
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	saved, err := c.SaveEntry(context.Background(), "acct-token", tracker.TrackEntry{
		RemoteID: "7224", Status: "current", Progress: 5,
	})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("SaveEntry issued %s, want POST", gotMethod)
	}
	if gotContentType != "application/vnd.api+json" {
		t.Fatalf("Content-Type = %q, want application/vnd.api+json", gotContentType)
	}

	data, _ := gotBody["data"].(map[string]any)
	if data["type"] != "library-entries" {
		t.Fatalf("data.type = %v, want library-entries", data["type"])
	}
	rel, _ := data["relationships"].(map[string]any)
	user, _ := rel["user"].(map[string]any)
	userData, _ := user["data"].(map[string]any)
	if userData["id"] != "555" {
		t.Fatalf("relationships.user.data.id = %v, want 555 (the self-resolved user id)", userData["id"])
	}
	media, _ := rel["media"].(map[string]any)
	mediaData, _ := media["data"].(map[string]any)
	if mediaData["id"] != "7224" || mediaData["type"] != "manga" {
		t.Fatalf("relationships.media.data = %+v, want id=7224 type=manga", mediaData)
	}

	if saved.RemoteID != "7224" || saved.LibraryID != "999" {
		t.Fatalf("SaveEntry result = %+v", saved)
	}
}

// TestClient_UpdateEntry_RequiresLibraryID confirms UpdateEntry refuses a
// blank LibraryID rather than silently creating a duplicate entry.
func TestClient_UpdateEntry_RequiresLibraryID(t *testing.T) {
	c := kitsu.New(nil)
	if _, err := c.UpdateEntry(context.Background(), "tok", tracker.TrackEntry{RemoteID: "7224"}); err == nil {
		t.Fatalf("UpdateEntry with blank LibraryID: want an error, got nil")
	}
}

// TestClient_UpdateEntry_SendsPATCHToLibraryEntryID pins UpdateEntry's HTTP
// method and target path.
func TestClient_UpdateEntry_SendsPATCHToLibraryEntryID(t *testing.T) {
	var gotMethod, gotPath string
	srv := kitsuAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/vnd.api+json")
		_, _ = w.Write([]byte(`{"data":{"id":"999","attributes":{"status":"current","progress":10},"relationships":{"manga":{"data":{"id":"7224","type":"manga"}}}}}`))
	})
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	if _, err := c.UpdateEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "7224", LibraryID: "999", Progress: 10}); err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Fatalf("UpdateEntry issued %s, want PATCH", gotMethod)
	}
	if gotPath != "/api/edge/library-entries/999" {
		t.Fatalf("UpdateEntry path = %q, want /api/edge/library-entries/999", gotPath)
	}
}

// TestClient_DeleteEntry_RequiresLibraryID mirrors
// TestClient_UpdateEntry_RequiresLibraryID for DeleteEntry.
func TestClient_DeleteEntry_RequiresLibraryID(t *testing.T) {
	c := kitsu.New(nil)
	if err := c.DeleteEntry(context.Background(), "tok", tracker.TrackEntry{RemoteID: "7224"}); err == nil {
		t.Fatalf("DeleteEntry with blank LibraryID: want an error, got nil")
	}
}

// TestClient_DeleteEntry_SendsDELETE pins DeleteEntry's HTTP method + path.
func TestClient_DeleteEntry_SendsDELETE(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	if err := c.DeleteEntry(context.Background(), "acct-token", tracker.TrackEntry{LibraryID: "999"}); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("DeleteEntry issued %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/edge/library-entries/999" {
		t.Fatalf("DeleteEntry path = %q, want /api/edge/library-entries/999", gotPath)
	}
}

// TestClient_UserAgent_SetOnEveryRequest is the mission-required test for
// the Cloudflare fix: it drives the token POST (LoginCredentials), a plain
// GET (Search), and a JSON:API mutation (SaveEntry, via the shared
// kitsuAPIServer) and asserts EVERY one carries the real browser
// browserUserAgent header — never Go's default "Go-http-client/1.1", which
// is the exact signature that triggered kitsu.app's Cloudflare 403.
func TestClient_UserAgent_SetOnEveryRequest(t *testing.T) {
	t.Run("token endpoint (LoginCredentials)", func(t *testing.T) {
		var gotUA string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUA = r.Header.Get("User-Agent")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))
		}))
		defer srv.Close()

		c := kitsu.New(newTestClient(t, srv))
		if _, err := c.LoginCredentials(context.Background(), "owner@example.test", "hunter2"); err != nil {
			t.Fatalf("LoginCredentials: %v", err)
		}
		assertBrowserUserAgent(t, gotUA)
	})

	t.Run("JSON:API GET (Search)", func(t *testing.T) {
		var gotUA string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUA = r.Header.Get("User-Agent")
			w.Header().Set("Content-Type", "application/vnd.api+json")
			_, _ = w.Write([]byte(`{"data":[]}`))
		}))
		defer srv.Close()

		c := kitsu.New(newTestClient(t, srv))
		if _, err := c.Search(context.Background(), "", "solo leveling"); err != nil {
			t.Fatalf("Search: %v", err)
		}
		assertBrowserUserAgent(t, gotUA)
	})

	t.Run("JSON:API mutation (SaveEntry)", func(t *testing.T) {
		var gotUA string
		srv := kitsuAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotUA = r.Header.Get("User-Agent")
			w.Header().Set("Content-Type", "application/vnd.api+json")
			_, _ = w.Write([]byte(`{"data":{"id":"999","attributes":{"status":"current","progress":5},"relationships":{"manga":{"data":{"id":"7224","type":"manga"}}}}}`))
		})
		defer srv.Close()

		c := kitsu.New(newTestClient(t, srv))
		if _, err := c.SaveEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "7224", Status: "current", Progress: 5}); err != nil {
			t.Fatalf("SaveEntry: %v", err)
		}
		assertBrowserUserAgent(t, gotUA)
	})
}

// assertBrowserUserAgent fails the test unless gotUA is the real browser
// User-Agent Client sends, and explicitly confirms it is NOT Go's default
// "Go-http-client/..." signature — the exact string that triggered
// kitsu.app's Cloudflare 403 in production.
func assertBrowserUserAgent(t *testing.T, gotUA string) {
	t.Helper()
	if strings.Contains(gotUA, "Go-http-client") {
		t.Fatalf("User-Agent = %q, still carries Go's default bot signature", gotUA)
	}
	if !strings.Contains(gotUA, "Chrome") {
		t.Fatalf("User-Agent = %q, want a browser UA containing \"Chrome\"", gotUA)
	}
}

// TestClient_HTTPNon200 confirms a non-2xx REST response fails the call.
func TestClient_HTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid token"))
	}))
	defer srv.Close()

	c := kitsu.New(newTestClient(t, srv))
	if _, err := c.Search(context.Background(), "bad-token", "q"); err == nil {
		t.Fatalf("Search against a 401: want an error, got nil")
	}
}

// TestClient_GetEntry_RequiresToken confirms GetEntry refuses an empty
// token rather than issuing an unauthenticated self-lookup that could never
// resolve "my" library entry.
func TestClient_GetEntry_RequiresToken(t *testing.T) {
	c := kitsu.New(nil)
	if _, err := c.GetEntry(context.Background(), "", "7224"); err == nil {
		t.Fatalf("GetEntry with empty token: want an error, got nil")
	}
}
