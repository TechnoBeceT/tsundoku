package anilist_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/anilist"
)

// TestAuthURL_ImplicitShape pins AniList's AuthURL to the REAL implicit-
// grant shape (re-verified against Suwayomi-Server's/Komikku's
// AnilistApi.kt authUrl()): response_type=token (no PKCE — an empty
// verifier), client_id present, and — the "unsupported_grant_type" login
// bug this fixes — NO redirect_uri, NO state, and NO client secret
// anywhere in the URL. state/redirectURI are passed in but must be IGNORED
// (see the Client.AuthURL doc comment: correlation moved to
// internal/tracker/connect's per-tracker pending stash).
func TestAuthURL_ImplicitShape(t *testing.T) {
	c := anilist.New("test-client-id", nil)

	authURL, verifier, err := c.AuthURL("csrf-state-123", "https://tsundoku.example/auth/tracker/callback")
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	if verifier != "" {
		t.Fatalf("AuthURL pkceVerifier = %q, want \"\" (AniList's implicit flow has no PKCE)", verifier)
	}

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("AuthURL returned an unparseable URL %q: %v", authURL, err)
	}
	if u.Host != "anilist.co" {
		t.Fatalf("AuthURL host = %q, want anilist.co", u.Host)
	}
	q := u.Query()
	assertQueryParam(t, q, "client_id", "test-client-id")
	assertQueryParam(t, q, "response_type", "token")
	if len(q) != 2 {
		t.Fatalf("AuthURL query = %v, want EXACTLY client_id+response_type (no redirect_uri, no state)", q)
	}
	if q.Has("code_challenge") {
		t.Fatalf("AuthURL carries a PKCE code_challenge — AniList's implicit flow must not use PKCE")
	}
	assertAuthURLDropsStateAndRedirectURI(t, authURL, q)
}

// assertAuthURLDropsStateAndRedirectURI is the shared "no state, no
// redirect_uri, no client secret" shape check both AniList's and MAL's
// AuthURL tests need — extracted so the driving tests stay under the
// fleet's per-function cyclomatic-complexity budget.
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

// TestAuthURL_BlankClientID confirms AuthURL fails closed
// (tracker.ErrClientIDNotConfigured) when this Client has no client id —
// the "blank disables this tracker" pattern.
func TestAuthURL_BlankClientID(t *testing.T) {
	c := anilist.New("", nil)
	if _, _, err := c.AuthURL("state", "https://example.test/callback"); !errors.Is(err, tracker.ErrClientIDNotConfigured) {
		t.Fatalf("AuthURL with blank client id: err = %v, want tracker.ErrClientIDNotConfigured", err)
	}
}

// TestExchangeCode_AlwaysImplicitFlowError confirms ExchangeCode never
// succeeds for AniList — its implicit grant has no server-exchangeable
// code.
func TestExchangeCode_AlwaysImplicitFlowError(t *testing.T) {
	c := anilist.New("test-client-id", nil)
	if _, err := c.ExchangeCode(context.Background(), "some-code", "", "https://example.test/callback"); !errors.Is(err, tracker.ErrImplicitFlow) {
		t.Fatalf("ExchangeCode: err = %v, want tracker.ErrImplicitFlow", err)
	}
}

// TestTokenFromImplicit_WrapsAccessToken confirms the fragment-extracted
// access token is wrapped into a TokenSet with a future ExpiresAt and no
// refresh token (AniList issues none).
func TestTokenFromImplicit_WrapsAccessToken(t *testing.T) {
	c := anilist.New("test-client-id", nil)
	before := time.Now()
	tok, err := c.TokenFromImplicit("frag-access-token")
	if err != nil {
		t.Fatalf("TokenFromImplicit: %v", err)
	}
	if tok.Access != "frag-access-token" {
		t.Fatalf("tok.Access = %q, want frag-access-token", tok.Access)
	}
	if tok.Refresh != "" {
		t.Fatalf("tok.Refresh = %q, want \"\" (AniList issues no refresh token)", tok.Refresh)
	}
	if tok.ExpiresAt == nil || !tok.ExpiresAt.After(before) {
		t.Fatalf("tok.ExpiresAt = %v, want a future time", tok.ExpiresAt)
	}
}

// TestTokenFromImplicit_EmptyTokenErrors confirms an empty access token is
// rejected rather than silently minting an unusable TokenSet.
func TestTokenFromImplicit_EmptyTokenErrors(t *testing.T) {
	c := anilist.New("test-client-id", nil)
	if _, err := c.TokenFromImplicit(""); err == nil {
		t.Fatalf("TokenFromImplicit(\"\"): want an error, got nil")
	}
}

// TestRefresh_AlwaysErrNoRefresh confirms AniList's Refresh never succeeds.
func TestRefresh_AlwaysErrNoRefresh(t *testing.T) {
	c := anilist.New("test-client-id", nil)
	if _, err := c.Refresh(context.Background(), "anything"); !errors.Is(err, tracker.ErrNoRefresh) {
		t.Fatalf("Refresh: err = %v, want tracker.ErrNoRefresh", err)
	}
}

// gqlEnvelope mirrors the client's own request shape for the test server to
// decode.
type gqlEnvelope struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// redirectTransport rewrites every outgoing request's scheme+host to
// target's, delegating everything else to http.DefaultTransport — the
// standard trick for unit-testing a client that POSTs to a hardcoded
// endpoint constant (graphQLEndpoint) against a local httptest.Server
// without changing production code. RoundTrip dials req.URL, not the
// original Host header, so this transparently redirects real-looking
// requests to the test server.
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

// newGraphQLTestServer returns an httptest.Server that decodes each
// request's GraphQL envelope, hands it to handle, and writes handle's
// returned raw JSON `data` payload back as {"data": <payload>}, capturing
// the last seen Authorization header into gotAuth. Used to drive
// Search/GetEntry/SaveEntry/UpdateEntry/DeleteEntry/AccountInfo against
// controlled responses without a real network call — AniList's real
// endpoint is unreachable at authoring time (spec §6).
func newGraphQLTestServer(t *testing.T, gotAuth *string, handle func(t *testing.T, env gqlEnvelope) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		var env gqlEnvelope
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			t.Fatalf("test server: decode request: %v", err)
		}
		payload := handle(t, env)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":` + payload + `}`))
	}))
}

// TestClient_Search_AttachesTokenAndParses drives Search against a fake
// GraphQL server, asserting the Bearer header is attached and the response
// maps to the shared TrackSearchResult shape.
func TestClient_Search_AttachesTokenAndParses(t *testing.T) {
	var gotAuth string
	srv := newGraphQLTestServer(t, &gotAuth, func(t *testing.T, env gqlEnvelope) string {
		if !strings.Contains(env.Query, "media(search:") {
			t.Fatalf("unexpected query sent to Search: %s", env.Query)
		}
		if env.Variables["search"] != "test manga" {
			t.Fatalf("search variable = %v, want %q", env.Variables["search"], "test manga")
		}
		return `{"Page":{"media":[{"id":42,"title":{"english":"Test Manga"},"coverImage":{"large":"https://x/y.jpg"},"status":"RELEASING","chapters":10,"siteUrl":"https://anilist.co/manga/42"}]}}`
	})
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	results, err := c.Search(context.Background(), "acct-token", "test manga")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "Bearer acct-token" {
		t.Fatalf("Authorization = %q, want Bearer acct-token", gotAuth)
	}
	if len(results) != 1 || results[0].RemoteID != "42" || results[0].Title != "Test Manga" || results[0].TotalChapters != 10 {
		t.Fatalf("Search results = %+v", results)
	}
}

// TestClient_GetEntry_NotYetTracked confirms a null MediaList (the account
// has not tracked this manga) maps to (nil, nil), not an error.
func TestClient_GetEntry_NotYetTracked(t *testing.T) {
	srv := newGraphQLTestServer(t, nil, func(t *testing.T, env gqlEnvelope) string {
		switch {
		case strings.Contains(env.Query, "Viewer"):
			return `{"Viewer":{"id":9,"name":"owner","mediaListOptions":{"scoreFormat":"POINT_100"}}}`
		case strings.Contains(env.Query, "MediaList(userId"):
			return `{"MediaList":null}`
		default:
			t.Fatalf("unexpected query: %s", env.Query)
			return "{}"
		}
	})
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	entry, err := c.GetEntry(context.Background(), "acct-token", "42")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry != nil {
		t.Fatalf("GetEntry = %+v, want nil (not yet tracked)", entry)
	}
}

// TestClient_GetEntry_RequiresToken confirms GetEntry refuses an empty
// token rather than issuing a doomed anonymous viewer lookup.
func TestClient_GetEntry_RequiresToken(t *testing.T) {
	c := anilist.New("cid", nil)
	if _, err := c.GetEntry(context.Background(), "", "42"); err == nil {
		t.Fatalf("GetEntry with empty token: want an error, got nil")
	}
}

// TestClient_SaveEntry_UpdateEntry_DeleteEntry drives the full create →
// update → delete cycle against a fake server, asserting each mutation's
// variables and the round-tripped TrackEntry shape.
func TestClient_SaveEntry_CreatesAndParses(t *testing.T) {
	srv := newGraphQLTestServer(t, nil, func(t *testing.T, env gqlEnvelope) string {
		if !strings.Contains(env.Query, "SaveMediaListEntry(mediaId:") {
			t.Fatalf("unexpected mutation: %s", env.Query)
		}
		assertVarFloat(t, env.Variables, "mediaId", 42)
		return `{"SaveMediaListEntry":{"id":100,"mediaId":42,"status":"CURRENT","score":0,"progress":5,"private":false,"startedAt":{},"completedAt":{}}}`
	})
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	saved, err := c.SaveEntry(context.Background(), "acct-token", tracker.TrackEntry{
		RemoteID: "42", Status: "CURRENT", Progress: 5,
	})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if saved.LibraryID != "100" || saved.RemoteID != "42" {
		t.Fatalf("SaveEntry result = %+v", saved)
	}
}

// TestClient_UpdateEntry_SendsScoreRawAndParses pins UpdateEntry's
// id-keyed (not mediaId-keyed) mutation shape, including the native-scale
// score conversion to AniList's scoreRaw variable.
func TestClient_UpdateEntry_SendsScoreRawAndParses(t *testing.T) {
	srv := newGraphQLTestServer(t, nil, func(t *testing.T, env gqlEnvelope) string {
		if !strings.Contains(env.Query, "SaveMediaListEntry(id:") {
			t.Fatalf("unexpected mutation: %s", env.Query)
		}
		assertVarFloat(t, env.Variables, "id", 100)
		assertVarFloat(t, env.Variables, "scoreRaw", 90)
		return `{"SaveMediaListEntry":{"id":100,"mediaId":42,"status":"COMPLETED","score":90,"progress":180,"private":false,"startedAt":{},"completedAt":{}}}`
	})
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	updated, err := c.UpdateEntry(context.Background(), "acct-token", tracker.TrackEntry{
		RemoteID: "42", LibraryID: "100", Status: "COMPLETED", Progress: 180, Score: 90,
	})
	if err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if updated.Status != "COMPLETED" || updated.Score != 90 {
		t.Fatalf("UpdateEntry result = %+v", updated)
	}
}

// TestClient_DeleteEntry_SendsLibraryID pins DeleteEntry's mutation shape.
func TestClient_DeleteEntry_SendsLibraryID(t *testing.T) {
	srv := newGraphQLTestServer(t, nil, func(t *testing.T, env gqlEnvelope) string {
		if !strings.Contains(env.Query, "DeleteMediaListEntry") {
			t.Fatalf("unexpected mutation: %s", env.Query)
		}
		assertVarFloat(t, env.Variables, "id", 100)
		return `{"DeleteMediaListEntry":{"deleted":true}}`
	})
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	entry := tracker.TrackEntry{RemoteID: "42", LibraryID: "100"}
	if err := c.DeleteEntry(context.Background(), "acct-token", entry); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
}

// assertVarFloat fails the test unless vars[key] equals want — GraphQL
// numeric variables decode through encoding/json as float64.
func assertVarFloat(t *testing.T, vars map[string]any, key string, want float64) {
	t.Helper()
	if got := vars[key]; got != want {
		t.Fatalf("variable %s = %v, want %v", key, got, want)
	}
}

// TestClient_UpdateEntry_RequiresLibraryID confirms UpdateEntry refuses a
// blank LibraryID rather than falling through to SaveEntry's mediaId-keyed
// upsert (which would silently create a DUPLICATE entry instead of erroring).
func TestClient_UpdateEntry_RequiresLibraryID(t *testing.T) {
	c := anilist.New("cid", nil)
	if _, err := c.UpdateEntry(context.Background(), "tok", tracker.TrackEntry{RemoteID: "42"}); err == nil {
		t.Fatalf("UpdateEntry with blank LibraryID: want an error, got nil")
	}
}

// TestClient_DeleteEntry_RequiresLibraryID mirrors
// TestClient_UpdateEntry_RequiresLibraryID for DeleteEntry.
func TestClient_DeleteEntry_RequiresLibraryID(t *testing.T) {
	c := anilist.New("cid", nil)
	if err := c.DeleteEntry(context.Background(), "tok", tracker.TrackEntry{RemoteID: "42"}); err == nil {
		t.Fatalf("DeleteEntry with blank LibraryID: want an error, got nil")
	}
}

// TestClient_AccountInfo_CapturesScoreFormat pins the login-time capture
// spec §4 requires (username + AniList score-format).
func TestClient_AccountInfo_CapturesScoreFormat(t *testing.T) {
	srv := newGraphQLTestServer(t, nil, func(t *testing.T, env gqlEnvelope) string {
		if !strings.Contains(env.Query, "Viewer") {
			t.Fatalf("unexpected query: %s", env.Query)
		}
		return `{"Viewer":{"id":9,"name":"owner","mediaListOptions":{"scoreFormat":"POINT_10_DECIMAL"}}}`
	})
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	info, err := c.AccountInfo(context.Background(), "acct-token")
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if info.RemoteUserID != "9" || info.Username != "owner" || info.ScoreFormat != "POINT_10_DECIMAL" {
		t.Fatalf("AccountInfo = %+v", info)
	}
}

// TestClient_GraphQLErrors_Fail confirms a non-empty "errors" array in the
// GraphQL envelope fails the call — a bad/expired token surfaces as an
// error, never a zero-value success.
func TestClient_GraphQLErrors_Fail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"Invalid token"}]}`))
	}))
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	if _, err := c.Search(context.Background(), "bad-token", "q"); err == nil {
		t.Fatalf("Search with a GraphQL error response: want an error, got nil")
	}
}

// TestClient_IdentityGetters pins the fixed Key/ID/Name/NeedsOAuth this
// Client reports in the tracker.Tracker contract.
func TestClient_IdentityGetters(t *testing.T) {
	c := anilist.New("cid", nil)
	if c.Key() != "anilist" {
		t.Fatalf("Key() = %q, want anilist", c.Key())
	}
	if c.ID() != tracker.IDAniList {
		t.Fatalf("ID() = %d, want tracker.IDAniList (%d)", c.ID(), tracker.IDAniList)
	}
	if c.Name() != "AniList" {
		t.Fatalf("Name() = %q, want AniList", c.Name())
	}
	if !c.NeedsOAuth() {
		t.Fatalf("NeedsOAuth() = false, want true")
	}
}

// TestClient_InvalidIDsError confirms every id-parsing entry point rejects a
// non-numeric id rather than silently sending a malformed GraphQL variable.
func TestClient_InvalidIDsError(t *testing.T) {
	c := anilist.New("cid", nil)

	if _, err := c.GetEntry(context.Background(), "tok", "not-a-number"); err == nil {
		t.Fatalf("GetEntry with a non-numeric remote id: want an error, got nil")
	}
	if _, err := c.SaveEntry(context.Background(), "tok", tracker.TrackEntry{RemoteID: "not-a-number"}); err == nil {
		t.Fatalf("SaveEntry with a non-numeric remote id: want an error, got nil")
	}
	if _, err := c.UpdateEntry(context.Background(), "tok", tracker.TrackEntry{RemoteID: "42", LibraryID: "not-a-number"}); err == nil {
		t.Fatalf("UpdateEntry with a non-numeric library id: want an error, got nil")
	}
	if err := c.DeleteEntry(context.Background(), "tok", tracker.TrackEntry{LibraryID: "not-a-number"}); err == nil {
		t.Fatalf("DeleteEntry with a non-numeric library id: want an error, got nil")
	}
}

// TestClient_AccountInfo_RequiresToken mirrors
// TestClient_GetEntry_RequiresToken for AccountInfo.
func TestClient_AccountInfo_RequiresToken(t *testing.T) {
	c := anilist.New("cid", nil)
	if _, err := c.AccountInfo(context.Background(), ""); err == nil {
		t.Fatalf("AccountInfo with empty token: want an error, got nil")
	}
}

// TestClient_AccountInfo_NilViewer confirms a null Viewer (an invalid/
// revoked token, per AniList's own behavior) surfaces as an error rather
// than a zero-value AccountInfo.
func TestClient_AccountInfo_NilViewer(t *testing.T) {
	srv := newGraphQLTestServer(t, nil, func(t *testing.T, env gqlEnvelope) string {
		return `{"Viewer":null}`
	})
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	if _, err := c.AccountInfo(context.Background(), "bad-token"); err == nil {
		t.Fatalf("AccountInfo with a null Viewer: want an error, got nil")
	}
}

// TestClient_HTTPNon200 confirms a non-200 HTTP response (as opposed to a
// GraphQL-level "errors" array) fails the call with the status surfaced.
func TestClient_HTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("upstream down"))
	}))
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	if _, err := c.Search(context.Background(), "tok", "q"); err == nil {
		t.Fatalf("Search against a 503: want an error, got nil")
	}
}

// TestClient_DecodeError confirms an unparseable response body fails
// cleanly rather than panicking.
func TestClient_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	c := anilist.New("cid", newTestClient(t, srv))
	if _, err := c.Search(context.Background(), "tok", "q"); err == nil {
		t.Fatalf("Search against a non-JSON body: want an error, got nil")
	}
}
