package mal_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/mal"
)

// TestAuthURL_PKCEPlainShape pins MAL's AuthURL to the REAL auth-code +
// PKCE-PLAIN shape (re-verified against Suwayomi-Server's/Komikku's
// MyAnimeListApi.kt authUrl()): code_challenge equal to the RETURNED
// verifier verbatim, NO code_challenge_method parameter at all (MAL
// defaults to plain when it's absent), and NO redirect_uri, NO state, NO
// client secret anywhere in the URL. state/redirectURI are passed in but
// must be IGNORED (see the Client.AuthURL doc comment: correlation moved to
// internal/tracker/connect's per-tracker pending stash).
func TestAuthURL_PKCEPlainShape(t *testing.T) {
	c := mal.New("test-client-id", "", nil)

	authURL, verifier, err := c.AuthURL("csrf-state-456", "https://tsundoku.example/auth/tracker/callback")
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	if verifier == "" {
		t.Fatalf("AuthURL returned an empty PKCE verifier — MAL requires PKCE")
	}

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("AuthURL returned an unparseable URL %q: %v", authURL, err)
	}
	if u.Host != "myanimelist.net" {
		t.Fatalf("AuthURL host = %q, want myanimelist.net", u.Host)
	}
	q := u.Query()
	assertQueryParam(t, q, "response_type", "code")
	assertQueryParam(t, q, "client_id", "test-client-id")
	assertQueryParam(t, q, "code_challenge", verifier)

	if len(q) != 3 {
		t.Fatalf("AuthURL query = %v, want EXACTLY response_type+client_id+code_challenge (no redirect_uri, no state)", q)
	}
	if q.Has("code_challenge_method") {
		t.Fatalf("AuthURL sent code_challenge_method=%q — PKCE-plain must OMIT this parameter entirely", q.Get("code_challenge_method"))
	}
	assertAuthURLDropsStateAndRedirectURI(t, authURL, q)
}

// assertAuthURLDropsStateAndRedirectURI is the shared "no state, no
// redirect_uri, no client secret" shape check — extracted so
// TestAuthURL_PKCEPlainShape stays under the fleet's per-function
// cyclomatic-complexity budget (mirrors the identically-named helper in
// internal/tracker/anilist's own client_test.go; kept as a small duplicate
// rather than a shared import since it is test-only and package-scoped).
func assertAuthURLDropsStateAndRedirectURI(t *testing.T, authURL string, q url.Values) {
	t.Helper()
	if q.Has("state") {
		t.Fatalf("AuthURL leaked a state param %q — the real endpoint rejects it (the unsupported_grant_type bug)", q.Get("state"))
	}
	if q.Has("redirect_uri") {
		t.Fatalf("AuthURL leaked a redirect_uri param %q — the real endpoint takes no redirect_uri", q.Get("redirect_uri"))
	}
	if q.Has("client_secret") || strings.Contains(authURL, "secret") {
		t.Fatalf("AuthURL leaked a client secret: %q", authURL)
	}
}

// assertQueryParam fails the test unless q's key parameter equals want —
// extracted so multi-field shape assertions (AuthURL, form bodies) don't
// each pile enough sequential `if`s to trip cyclop's per-function
// complexity budget.
func assertQueryParam(t *testing.T, q url.Values, key, want string) {
	t.Helper()
	if got := q.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

// TestAuthURL_GeneratesFreshVerifierEachCall confirms two AuthURL calls
// never reuse the same PKCE verifier — a shared verifier would let one
// login's proof be replayed against another.
func TestAuthURL_GeneratesFreshVerifierEachCall(t *testing.T) {
	c := mal.New("test-client-id", "", nil)
	_, v1, err := c.AuthURL("state-a", "https://example.test/callback")
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	_, v2, err := c.AuthURL("state-b", "https://example.test/callback")
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	if v1 == v2 {
		t.Fatalf("AuthURL produced the same PKCE verifier twice: %q", v1)
	}
}

// TestAuthURL_BlankClientID confirms AuthURL fails closed
// (tracker.ErrClientIDNotConfigured) when this Client has no client id.
func TestAuthURL_BlankClientID(t *testing.T) {
	c := mal.New("", "", nil)
	if _, _, err := c.AuthURL("state", "https://example.test/callback"); !errors.Is(err, tracker.ErrClientIDNotConfigured) {
		t.Fatalf("AuthURL with blank client id: err = %v, want tracker.ErrClientIDNotConfigured", err)
	}
}

// redirectTransport rewrites every outgoing request's scheme+host to
// target's — see internal/tracker/anilist/client_test.go's identical
// helper for the full rationale (redirecting a client that POSTs to a
// hardcoded endpoint constant to a local httptest.Server).
type redirectTransport struct {
	target *url.URL
}

func newTestClient(t *testing.T, srv *httptest.Server) *http.Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return &http.Client{Transport: &redirectTransport{target: u}}
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// TestExchangeCode_RequestBodyShape is the mission-required test: it drives
// ExchangeCode against a fake token server and asserts the POSTed FORM
// BODY carries EXACTLY the fields MAL's real PKCE-plain auth-code grant
// needs (client_id, grant_type=authorization_code, code, code_verifier ==
// pkceVerifier verbatim) — re-verified against Suwayomi-Server's/Komikku's
// MyAnimeListApi.kt getAccessToken(), neither of which sends redirect_uri
// or a client_secret — and that the parsed TokenSet carries
// access/refresh/expiry correctly.
func TestExchangeCode_RequestBodyShape(t *testing.T) {
	var gotForm url.Values
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	c := mal.New("test-client-id", "", newTestClient(t, srv))
	tok, err := c.ExchangeCode(context.Background(), "the-auth-code", "the-pkce-verifier", "https://tsundoku.example/auth/tracker/callback")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}

	if gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q, want application/x-www-form-urlencoded", gotContentType)
	}
	assertQueryParam(t, gotForm, "client_id", "test-client-id")
	assertQueryParam(t, gotForm, "grant_type", "authorization_code")
	assertQueryParam(t, gotForm, "code", "the-auth-code")
	assertQueryParam(t, gotForm, "code_verifier", "the-pkce-verifier")
	if len(gotForm) != 4 {
		t.Fatalf("ExchangeCode form = %v, want EXACTLY client_id+grant_type+code+code_verifier (no redirect_uri)", gotForm)
	}
	if gotForm.Has("redirect_uri") {
		t.Fatalf("form leaked a redirect_uri field %q — MAL's token endpoint takes no redirect_uri", gotForm.Get("redirect_uri"))
	}
	if gotForm.Has("client_secret") {
		t.Fatalf("form leaked a client_secret field")
	}

	assertTokenSet(t, tok, "new-access", "new-refresh")
}

// assertTokenSet fails the test unless tok carries wantAccess/wantRefresh
// and a future ExpiresAt.
func assertTokenSet(t *testing.T, tok tracker.TokenSet, wantAccess, wantRefresh string) {
	t.Helper()
	if tok.Access != wantAccess || tok.Refresh != wantRefresh {
		t.Fatalf("TokenSet = %+v, want Access=%s Refresh=%s", tok, wantAccess, wantRefresh)
	}
	if tok.ExpiresAt == nil || !tok.ExpiresAt.After(time.Now()) {
		t.Fatalf("TokenSet.ExpiresAt = %v, want a future time", tok.ExpiresAt)
	}
}

// TestRefresh_RequestBodyShape mirrors TestExchangeCode_RequestBodyShape
// for the refresh_token grant.
func TestRefresh_RequestBodyShape(t *testing.T) {
	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"refreshed-access","refresh_token":"refreshed-refresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	c := mal.New("test-client-id", "", newTestClient(t, srv))
	tok, err := c.Refresh(context.Background(), "old-refresh-token")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	if gotForm.Get("grant_type") != "refresh_token" {
		t.Fatalf("form grant_type = %q, want refresh_token", gotForm.Get("grant_type"))
	}
	if gotForm.Get("refresh_token") != "old-refresh-token" {
		t.Fatalf("form refresh_token = %q, want old-refresh-token", gotForm.Get("refresh_token"))
	}
	if tok.Access != "refreshed-access" {
		t.Fatalf("TokenSet.Access = %q, want refreshed-access", tok.Access)
	}
	if gotForm.Has("client_secret") {
		t.Fatalf("form leaked a client_secret field — this Client was built with an empty secret (public app)")
	}
}

// TestClientSecret_SentOnlyWhenConfigured is the mission-required test for
// the confidential-app fix: it drives BOTH ExchangeCode and Refresh against
// a fake token server with a Client built WITH a client secret and a Client
// built WITHOUT one, asserting the POSTed form carries client_secret in the
// former case and has NO client_secret KEY AT ALL (not merely an empty
// value) in the latter — a public/"other"-type MAL app must never send an
// empty client_secret field, since that is a different request shape than
// omitting it.
func TestClientSecret_SentOnlyWhenConfigured(t *testing.T) {
	tests := []struct {
		name         string
		clientSecret string
		wantPresent  bool
	}{
		{name: "confidential app sends client_secret", clientSecret: "the-app-secret", wantPresent: true},
		{name: "public app omits client_secret entirely", clientSecret: "", wantPresent: false},
	}

	for _, tt := range tests {
		t.Run(tt.name+"/ExchangeCode", func(t *testing.T) {
			var gotForm url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				gotForm, _ = url.ParseQuery(string(body))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))
			}))
			defer srv.Close()

			c := mal.New("cid", tt.clientSecret, newTestClient(t, srv))
			if _, err := c.ExchangeCode(context.Background(), "code", "verifier", "https://example.test/cb"); err != nil {
				t.Fatalf("ExchangeCode: %v", err)
			}
			assertClientSecretPresence(t, gotForm, tt.clientSecret, tt.wantPresent)
		})

		t.Run(tt.name+"/Refresh", func(t *testing.T) {
			var gotForm url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				gotForm, _ = url.ParseQuery(string(body))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))
			}))
			defer srv.Close()

			c := mal.New("cid", tt.clientSecret, newTestClient(t, srv))
			if _, err := c.Refresh(context.Background(), "old-refresh"); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
			assertClientSecretPresence(t, gotForm, tt.clientSecret, tt.wantPresent)
		})
	}
}

// assertClientSecretPresence fails the test unless gotForm's client_secret
// KEY presence matches wantPresent exactly — Has(), not Get(), so an
// accidentally-sent empty-value field would still fail this (the bug this
// guards against is sending client_secret="" for a public app, not just a
// wrong value).
func assertClientSecretPresence(t *testing.T, gotForm url.Values, secret string, wantPresent bool) {
	t.Helper()
	if got := gotForm.Has("client_secret"); got != wantPresent {
		t.Fatalf("form.Has(client_secret) = %v, want %v", got, wantPresent)
	}
	if wantPresent && gotForm.Get("client_secret") != secret {
		t.Fatalf("form client_secret = %q, want %q", gotForm.Get("client_secret"), secret)
	}
}

// TestRefresh_EmptyTokenIsErrNoRefresh confirms Refresh never issues a
// network call for an empty refresh token — MAL always errors on that
// anyway, but this fails fast and cleanly with the shared sentinel.
func TestRefresh_EmptyTokenIsErrNoRefresh(t *testing.T) {
	c := mal.New("test-client-id", "", nil)
	if _, err := c.Refresh(context.Background(), ""); !errors.Is(err, tracker.ErrNoRefresh) {
		t.Fatalf("Refresh(\"\"): err = %v, want tracker.ErrNoRefresh", err)
	}
}

// TestClient_Search_AttachesBearerAndParses drives Search against a fake
// REST server, asserting the Bearer header and the mapped
// TrackSearchResult shape.
func TestClient_Search_AttachesBearerAndParses(t *testing.T) {
	var gotAuth string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"node":{"id":887,"title":"Berserk","main_picture":{"large":"https://x/y.jpg"},"num_chapters":0,"status":"currently_publishing"}}]}`))
	}))
	defer srv.Close()

	c := mal.New("cid", "", newTestClient(t, srv))
	results, err := c.Search(context.Background(), "acct-token", "berserk")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "Bearer acct-token" {
		t.Fatalf("Authorization = %q, want Bearer acct-token", gotAuth)
	}
	if gotPath != "/v2/manga" {
		t.Fatalf("path = %q, want /v2/manga", gotPath)
	}
	if len(results) != 1 || results[0].RemoteID != "887" || results[0].Title != "Berserk" {
		t.Fatalf("Search results = %+v", results)
	}
}

// TestClient_GetEntry_NotYetTracked confirms an absent my_list_status maps
// to (nil, nil).
func TestClient_GetEntry_NotYetTracked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"title":"Untracked","num_chapters":10}`))
	}))
	defer srv.Close()

	c := mal.New("cid", "", newTestClient(t, srv))
	entry, err := c.GetEntry(context.Background(), "acct-token", "1")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry != nil {
		t.Fatalf("GetEntry = %+v, want nil (not yet tracked)", entry)
	}
}

// upsertTestServer builds an httptest.Server standing in for MAL's
// my_list_status endpoint: any PUT echoes a representative myListStatus
// JSON body (capturing the request's form for the caller to inspect via
// lastMethod/lastForm), any other method (DELETE) answers 200 with an
// empty body. Shared by the Save/Update/Delete tests below.
func upsertTestServer(t *testing.T, lastMethod *string, lastForm *url.Values) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*lastMethod = r.Method
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		*lastForm, _ = url.ParseQuery(string(body))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"reading","score":7,"num_chapters_read":42,"start_date":"2024-01-01","finish_date":""}`))
	}))
}

// TestClient_SaveEntry_SendsPUTAndParses pins SaveEntry's upsert wire shape.
func TestClient_SaveEntry_SendsPUTAndParses(t *testing.T) {
	var lastMethod string
	var lastForm url.Values
	srv := upsertTestServer(t, &lastMethod, &lastForm)
	defer srv.Close()

	c := mal.New("cid", "", newTestClient(t, srv))
	saved, err := c.SaveEntry(context.Background(), "acct-token", tracker.TrackEntry{
		RemoteID: "887", Status: "reading", Score: 7, Progress: 42,
	})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if lastMethod != http.MethodPut {
		t.Fatalf("SaveEntry issued %s, want PUT", lastMethod)
	}
	assertQueryParam(t, lastForm, "status", "reading")
	assertQueryParam(t, lastForm, "score", "7")
	assertQueryParam(t, lastForm, "num_chapters_read", "42")
	if saved.RemoteID != "887" || saved.Progress != 42 {
		t.Fatalf("SaveEntry result = %+v", saved)
	}
}

// TestClient_UpdateEntry_SendsPUT confirms UpdateEntry issues the SAME
// PUT-upsert call as SaveEntry (MAL has no separate list-entry id to key
// an update by).
func TestClient_UpdateEntry_SendsPUT(t *testing.T) {
	var lastMethod string
	var lastForm url.Values
	srv := upsertTestServer(t, &lastMethod, &lastForm)
	defer srv.Close()

	c := mal.New("cid", "", newTestClient(t, srv))
	entry := tracker.TrackEntry{RemoteID: "887", Status: "reading", Score: 7, Progress: 42}
	if _, err := c.UpdateEntry(context.Background(), "acct-token", entry); err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if lastMethod != http.MethodPut {
		t.Fatalf("UpdateEntry issued %s, want PUT", lastMethod)
	}
}

// TestClient_DeleteEntry_SendsDELETE pins DeleteEntry's HTTP method.
func TestClient_DeleteEntry_SendsDELETE(t *testing.T) {
	var lastMethod string
	var lastForm url.Values
	srv := upsertTestServer(t, &lastMethod, &lastForm)
	defer srv.Close()

	c := mal.New("cid", "", newTestClient(t, srv))
	entry := tracker.TrackEntry{RemoteID: "887"}
	if err := c.DeleteEntry(context.Background(), "acct-token", entry); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if lastMethod != http.MethodDelete {
		t.Fatalf("DeleteEntry issued %s, want DELETE", lastMethod)
	}
}

// TestClient_UpsertEntry_RequiresRemoteID confirms both SaveEntry and
// UpdateEntry refuse a blank RemoteID (MAL keys every write by manga id
// alone — there is no separate list-entry id to fall back on).
func TestClient_UpsertEntry_RequiresRemoteID(t *testing.T) {
	c := mal.New("cid", "", nil)
	if _, err := c.SaveEntry(context.Background(), "tok", tracker.TrackEntry{}); err == nil {
		t.Fatalf("SaveEntry with blank RemoteID: want an error, got nil")
	}
	if _, err := c.UpdateEntry(context.Background(), "tok", tracker.TrackEntry{}); err == nil {
		t.Fatalf("UpdateEntry with blank RemoteID: want an error, got nil")
	}
	if err := c.DeleteEntry(context.Background(), "tok", tracker.TrackEntry{}); err == nil {
		t.Fatalf("DeleteEntry with blank RemoteID: want an error, got nil")
	}
}

// TestClient_HTTPNon200 confirms a non-200 REST response fails the call.
func TestClient_HTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid token"))
	}))
	defer srv.Close()

	c := mal.New("cid", "", newTestClient(t, srv))
	if _, err := c.Search(context.Background(), "bad-token", "q"); err == nil {
		t.Fatalf("Search against a 401: want an error, got nil")
	}
}

// TestClient_TokenEndpointNon200 confirms a non-200 token-endpoint response
// fails ExchangeCode/Refresh cleanly.
func TestClient_TokenEndpointNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	c := mal.New("cid", "", newTestClient(t, srv))
	if _, err := c.ExchangeCode(context.Background(), "code", "verifier", "https://example.test/cb"); err == nil {
		t.Fatalf("ExchangeCode against a 400: want an error, got nil")
	}
}

// TestClient_IdentityGetters pins the fixed Key/ID/Name/NeedsOAuth this
// Client reports in the tracker.Tracker contract.
func TestClient_IdentityGetters(t *testing.T) {
	c := mal.New("cid", "", nil)
	if c.Key() != "mal" {
		t.Fatalf("Key() = %q, want mal", c.Key())
	}
	if c.ID() != tracker.IDMAL {
		t.Fatalf("ID() = %d, want tracker.IDMAL (%d)", c.ID(), tracker.IDMAL)
	}
	if c.Name() != "MyAnimeList" {
		t.Fatalf("Name() = %q, want MyAnimeList", c.Name())
	}
	if !c.NeedsOAuth() {
		t.Fatalf("NeedsOAuth() = false, want true")
	}
}
