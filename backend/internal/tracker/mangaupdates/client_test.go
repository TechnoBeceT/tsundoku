package mangaupdates_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/mangaupdates"
)

// redirectTransport rewrites every outgoing request's scheme+host to
// target's — needed because mangaupdates.Client posts to a hardcoded
// baseURL constant; mirrors internal/tracker/mal and
// internal/tracker/kitsu's identical test helper.
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
	c := mangaupdates.New(nil)
	if c.Key() != "mangaupdates" {
		t.Fatalf("Key() = %q, want mangaupdates", c.Key())
	}
	if c.ID() != tracker.IDMangaUpdates {
		t.Fatalf("ID() = %d, want tracker.IDMangaUpdates (%d)", c.ID(), tracker.IDMangaUpdates)
	}
	if c.Name() != "MangaUpdates" {
		t.Fatalf("Name() = %q, want MangaUpdates", c.Name())
	}
	if c.NeedsOAuth() {
		t.Fatalf("NeedsOAuth() = true, want false — MangaUpdates connects via LoginCredentials")
	}
}

// TestAuthURL_ReturnsErrOAuthNotSupported confirms MangaUpdates (a
// credential-login tracker) fails closed on the OAuth-redirect surface.
func TestAuthURL_ReturnsErrOAuthNotSupported(t *testing.T) {
	c := mangaupdates.New(nil)
	if _, _, err := c.AuthURL("state", "https://example.test/callback"); !errors.Is(err, tracker.ErrOAuthNotSupported) {
		t.Fatalf("AuthURL: err = %v, want tracker.ErrOAuthNotSupported", err)
	}
	if _, err := c.ExchangeCode(context.Background(), "code", "verifier", "https://example.test/cb"); !errors.Is(err, tracker.ErrOAuthNotSupported) {
		t.Fatalf("ExchangeCode: err = %v, want tracker.ErrOAuthNotSupported", err)
	}
}

// TestRefresh_AlwaysErrNoRefresh confirms Refresh never issues a network
// call — MangaUpdates has no refresh grant at all.
func TestRefresh_AlwaysErrNoRefresh(t *testing.T) {
	c := mangaupdates.New(nil)
	if _, err := c.Refresh(context.Background(), "anything"); !errors.Is(err, tracker.ErrNoRefresh) {
		t.Fatalf("Refresh: err = %v, want tracker.ErrNoRefresh", err)
	}
}

// TestLoginCredentials_RequestBodyShape pins PUT /v1/account/login's JSON
// body and success-response parsing.
func TestLoginCredentials_RequestBodyShape(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","context":{"session_token":"mu-session-token","uid":42}}`))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	tok, err := c.LoginCredentials(context.Background(), "owner@example.test", "hunter2")
	if err != nil {
		t.Fatalf("LoginCredentials: %v", err)
	}

	if gotMethod != http.MethodPut {
		t.Fatalf("LoginCredentials issued %s, want PUT", gotMethod)
	}
	if gotPath != "/v1/account/login" {
		t.Fatalf("LoginCredentials path = %q, want /v1/account/login", gotPath)
	}
	if gotBody["username"] != "owner@example.test" || gotBody["password"] != "hunter2" {
		t.Fatalf("LoginCredentials body = %+v", gotBody)
	}
	if tok.Access != "mu-session-token" {
		t.Fatalf("TokenSet.Access = %q, want mu-session-token", tok.Access)
	}
	if tok.Refresh != "" {
		t.Fatalf("TokenSet.Refresh = %q, want \"\" (MangaUpdates issues no refresh token)", tok.Refresh)
	}
}

// TestLoginCredentials_FailureStatus confirms a non-"success" status body
// fails the login even on a 200 response — MangaUpdates reports login
// failures in-body, not via HTTP status.
func TestLoginCredentials_FailureStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"error","context":{}}`))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	if _, err := c.LoginCredentials(context.Background(), "owner@example.test", "wrong"); err == nil {
		t.Fatalf("LoginCredentials with a failure status: want an error, got nil")
	}
}

// TestClient_Search_RequestBodyShapeAndParses pins the search request body
// and the mapped TrackSearchResult shape.
func TestClient_Search_RequestBodyShapeAndParses(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"record":{"series_id":12345,"title":"Solo Leveling","url":"https://www.mangaupdates.com/series/12345","image":{"url":{"original":"https://x/y.jpg"}},"status":"Complete"}}]}`))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	results, err := c.Search(context.Background(), "", "solo leveling")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/series/search" {
		t.Fatalf("Search request = %s %s, want POST /v1/series/search", gotMethod, gotPath)
	}
	if gotBody["search"] != "solo leveling" {
		t.Fatalf("Search body[search] = %v, want %q", gotBody["search"], "solo leveling")
	}
	if len(results) != 1 || results[0].RemoteID != "12345" || results[0].Title != "Solo Leveling" {
		t.Fatalf("Search results = %+v", results)
	}
}

// TestClient_GetEntry_NotFoundMapsToNilNil confirms a 404 (not on the
// Reading List) maps to (nil, nil), never an error.
func TestClient_GetEntry_NotFoundMapsToNilNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	entry, err := c.GetEntry(context.Background(), "acct-token", "12345")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry != nil {
		t.Fatalf("GetEntry = %+v, want nil (not on the list)", entry)
	}
}

// TestClient_GetEntry_Found pins the GET path — NO list-id URL segment
// (see the package doc comment) — and response mapping. The nested
// "series" object's id key is "id", not "series_id" (that key belongs only
// to the unrelated /series/search response).
func TestClient_GetEntry_Found(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"list_id":0,"series":{"id":12345,"title":"Solo Leveling"},"status":{"volume":0,"chapter":42}}`))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	entry, err := c.GetEntry(context.Background(), "acct-token", "12345")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if gotPath != "/v1/lists/series/12345" {
		t.Fatalf("GetEntry path = %q, want /v1/lists/series/12345", gotPath)
	}
	if entry == nil || entry.RemoteID != "12345" || entry.Progress != 42 || entry.Status != "reading" {
		t.Fatalf("GetEntry = %+v", entry)
	}
}

// TestClient_SaveEntry_RequestBodyShape pins POST /v1/lists/series's JSON
// body — an ARRAY of {series:{id}, list_id} and, UNLIKE UpdateEntry, NO
// status/chapter object (mirrors the reference ports' addSeriesToList,
// which never sends progress on bind).
func TestClient_SaveEntry_RequestBodyShape(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"list_id":0,"series":{"id":12345,"title":"Solo Leveling"},"status":{"chapter":0}}]`))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	saved, err := c.SaveEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "12345"})
	if err != nil {
		t.Fatalf("SaveEntry: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/lists/series" {
		t.Fatalf("SaveEntry request = %s %s, want POST /v1/lists/series", gotMethod, gotPath)
	}
	if len(gotBody) != 1 {
		t.Fatalf("SaveEntry body = %+v, want one entry", gotBody)
	}
	series, _ := gotBody[0]["series"].(map[string]any)
	if int64(series["id"].(float64)) != 12345 {
		t.Fatalf("SaveEntry body[0].series = %+v, want id=12345", series)
	}
	if listID, ok := gotBody[0]["list_id"].(float64); !ok || int64(listID) != 0 {
		t.Fatalf("SaveEntry body[0].list_id = %v, want 0", gotBody[0]["list_id"])
	}
	if _, hasStatus := gotBody[0]["status"]; hasStatus {
		t.Fatalf("SaveEntry body[0] = %+v, want NO status object on bind", gotBody[0])
	}
	if saved.RemoteID != "12345" {
		t.Fatalf("SaveEntry result = %+v", saved)
	}
}

// TestClient_UpdateEntry_RequestBodyShape pins POST /v1/lists/series/update
// — the distinct URL from SaveEntry's bare /v1/lists/series — and the body
// shape {series:{id}, list_id, status:{chapter}} (UpdateEntry, unlike
// SaveEntry, DOES send progress).
func TestClient_UpdateEntry_RequestBodyShape(t *testing.T) {
	var gotPath string
	var gotBody []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"list_id":0,"series":{"id":12345},"status":{"chapter":10}}]`))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	updated, err := c.UpdateEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "12345", Progress: 10})
	if err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if gotPath != "/v1/lists/series/update" {
		t.Fatalf("UpdateEntry path = %q, want /v1/lists/series/update", gotPath)
	}
	if len(gotBody) != 1 {
		t.Fatalf("UpdateEntry body = %+v, want one entry", gotBody)
	}
	series, _ := gotBody[0]["series"].(map[string]any)
	if int64(series["id"].(float64)) != 12345 {
		t.Fatalf("UpdateEntry body[0].series = %+v, want id=12345", series)
	}
	if listID, ok := gotBody[0]["list_id"].(float64); !ok || int64(listID) != 0 {
		t.Fatalf("UpdateEntry body[0].list_id = %v, want 0", gotBody[0]["list_id"])
	}
	status, _ := gotBody[0]["status"].(map[string]any)
	if int64(status["chapter"].(float64)) != 10 {
		t.Fatalf("UpdateEntry body[0].status = %+v, want chapter=10", status)
	}
	if updated.RemoteID != "12345" || updated.Progress != 10 {
		t.Fatalf("UpdateEntry result = %+v", updated)
	}
}

// TestClient_UpdateEntry_CompletedStatusTargetsCompleteList pins the BUG-4
// completion path: an UpdateEntry whose Status is MangaUpdates' native
// "complete" label targets the Complete list (list_id 2) — MangaUpdates has no
// status STRING, so completing an entry means moving it to that list. This is
// what lets syncsvc.CompleteSeries genuinely complete a MangaUpdates entry (a
// tracker that never reports a total and so can't auto-complete on its own).
func TestClient_UpdateEntry_CompletedStatusTargetsCompleteList(t *testing.T) {
	var gotBody []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"list_id":2,"series":{"id":12345},"status":{"chapter":83}}]`))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	if _, err := c.UpdateEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "12345", Progress: 83, Status: "complete"}); err != nil {
		t.Fatalf("UpdateEntry: %v", err)
	}
	if len(gotBody) != 1 {
		t.Fatalf("UpdateEntry body = %+v, want one entry", gotBody)
	}
	if listID, ok := gotBody[0]["list_id"].(float64); !ok || int64(listID) != 2 {
		t.Fatalf("UpdateEntry body[0].list_id = %v, want 2 (the Complete list)", gotBody[0]["list_id"])
	}
}

// muWriteResponse is one MangaUpdates list-WRITE response shape a test drives
// the fake server to answer with — the WHOLE point of the over-decode fix is
// that SaveEntry/UpdateEntry succeed on EVERY one of these (a status
// envelope OBJECT, an empty 200 body, and an empty 204 body), none of which is
// the []listSeriesEntry array the client used to strictly decode into.
type muWriteResponse struct {
	name       string
	statusCode int
	body       string
}

func muWriteResponses() []muWriteResponse {
	return []muWriteResponse{
		{name: "status envelope object", statusCode: http.StatusOK, body: `{"status":"success","context":{"series_id":12345}}`},
		{name: "empty 200 body", statusCode: http.StatusOK, body: ""},
		{name: "empty 204 body", statusCode: http.StatusNoContent, body: ""},
	}
}

// TestClient_SaveEntry_ToleratesEnvelopeAndEmptyResponses is the STEP-2
// regression proof for SaveEntry: MangaUpdates' bind write answers with a
// status envelope or an empty body, NOT the series array — the client must
// succeed on all of them and return the caller's own entry (RemoteID kept).
func TestClient_SaveEntry_ToleratesEnvelopeAndEmptyResponses(t *testing.T) {
	for _, tc := range muWriteResponses() {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := mangaupdates.New(newTestClient(t, srv))
			saved, err := c.SaveEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "12345"})
			if err != nil {
				t.Fatalf("SaveEntry against %s: unexpected error: %v", tc.name, err)
			}
			if saved.RemoteID != "12345" {
				t.Fatalf("SaveEntry result = %+v, want the caller's own RemoteID kept", saved)
			}
		})
	}
}

// TestClient_UpdateEntry_ToleratesEnvelopeAndEmptyResponses is the STEP-2
// regression proof for UpdateEntry (the chapter-progress push): the same three
// non-array write responses must all succeed and return the caller's entry.
func TestClient_UpdateEntry_ToleratesEnvelopeAndEmptyResponses(t *testing.T) {
	for _, tc := range muWriteResponses() {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := mangaupdates.New(newTestClient(t, srv))
			updated, err := c.UpdateEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "12345", Progress: 10})
			if err != nil {
				t.Fatalf("UpdateEntry against %s: unexpected error: %v", tc.name, err)
			}
			if updated.RemoteID != "12345" || updated.Progress != 10 {
				t.Fatalf("UpdateEntry result = %+v, want the caller's own RemoteID/Progress kept", updated)
			}
		})
	}
}

// TestClient_DeleteEntry_RequestBodyShape pins the delete endpoint — no
// list-id URL segment — and the body: a BARE JSON array of series ids, not
// an array of {series:{id}} objects (mirrors the reference ports'
// deleteSeriesFromList).
func TestClient_DeleteEntry_RequestBodyShape(t *testing.T) {
	var gotPath string
	var gotBody []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	if err := c.DeleteEntry(context.Background(), "acct-token", tracker.TrackEntry{RemoteID: "12345"}); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	if gotPath != "/v1/lists/series/delete" {
		t.Fatalf("DeleteEntry path = %q, want /v1/lists/series/delete", gotPath)
	}
	if len(gotBody) != 1 || gotBody[0] != 12345 {
		t.Fatalf("DeleteEntry body = %+v, want [12345]", gotBody)
	}
}

// TestClient_ReactiveReloginOn401RetriesOnce is the STEP-4 core proof: an
// authenticated call whose session token 401s triggers a single re-login (via
// the wired Reauthenticator) and retries ONCE with the fresh token, succeeding
// — the caller never sees the transient 401.
func TestClient_ReactiveReloginOn401RetriesOnce(t *testing.T) {
	var authHeaders []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		if len(authHeaders) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	var reloginCalls int
	c := mangaupdates.New(newTestClient(t, srv))
	c.SetReauthenticator(func(context.Context) (string, error) {
		reloginCalls++
		return "fresh-token", nil
	})

	if _, err := c.UpdateEntry(context.Background(), "stale-token", tracker.TrackEntry{RemoteID: "12345", Progress: 10}); err != nil {
		t.Fatalf("UpdateEntry after reactive re-login: unexpected error: %v", err)
	}
	if reloginCalls != 1 {
		t.Fatalf("re-login calls = %d, want exactly 1", reloginCalls)
	}
	if len(authHeaders) != 2 {
		t.Fatalf("request count = %d, want 2 (original 401 + one retry)", len(authHeaders))
	}
	if authHeaders[0] != "Bearer stale-token" {
		t.Fatalf("first request Authorization = %q, want the stale token", authHeaders[0])
	}
	if authHeaders[1] != "Bearer fresh-token" {
		t.Fatalf("retry Authorization = %q, want the freshly re-logged-in token", authHeaders[1])
	}
}

// TestClient_401WithoutReauthIsTokenExpired confirms a 401 with NO
// Reauthenticator wired surfaces as tracker.ErrTokenExpired (so the
// orchestration layer flags the connection for a manual reconnect), never a
// generic error.
func TestClient_401WithoutReauthIsTokenExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	_, err := c.UpdateEntry(context.Background(), "stale-token", tracker.TrackEntry{RemoteID: "12345", Progress: 10})
	if !errors.Is(err, tracker.ErrTokenExpired) {
		t.Fatalf("UpdateEntry 401 without reauth: err = %v, want tracker.ErrTokenExpired", err)
	}
}

// TestClient_401ReauthFailsSurfacesTokenExpired confirms that when the reactive
// re-login itself fails, the persistent 401 still surfaces as
// tracker.ErrTokenExpired (bounded to one retry, no loop) — GetEntry path.
func TestClient_401ReauthFailsSurfacesTokenExpired(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	c.SetReauthenticator(func(context.Context) (string, error) {
		return "", errors.New("re-login rejected")
	})

	_, err := c.GetEntry(context.Background(), "stale-token", "12345")
	if !errors.Is(err, tracker.ErrTokenExpired) {
		t.Fatalf("GetEntry with a failing re-login: err = %v, want tracker.ErrTokenExpired", err)
	}
	// One original request only — a FAILED re-login must not resend (no retry,
	// no loop).
	if calls != 1 {
		t.Fatalf("request count = %d, want 1 (failed re-login must not retry)", calls)
	}
}

// TestClient_UpsertAndDelete_RequireRemoteID confirms SaveEntry/
// UpdateEntry/DeleteEntry all refuse a blank RemoteID.
func TestClient_UpsertAndDelete_RequireRemoteID(t *testing.T) {
	c := mangaupdates.New(nil)
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

// TestClient_GetEntry_RequiresToken confirms GetEntry refuses an empty
// token.
func TestClient_GetEntry_RequiresToken(t *testing.T) {
	c := mangaupdates.New(nil)
	if _, err := c.GetEntry(context.Background(), "", "12345"); err == nil {
		t.Fatalf("GetEntry with empty token: want an error, got nil")
	}
}

// TestClient_HTTPNon200 confirms a non-2xx (non-404) REST response fails
// the call.
func TestClient_HTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	c := mangaupdates.New(newTestClient(t, srv))
	if _, err := c.Search(context.Background(), "", "q"); err == nil {
		t.Fatalf("Search against a 500: want an error, got nil")
	}
}
