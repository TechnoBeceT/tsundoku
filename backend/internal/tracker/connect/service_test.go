package connect_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/connect"
)

// fakeCodeTracker is an auth-code (MAL-shaped) tracker.Tracker test double
// — ExchangeCode succeeds deterministically from the code it is given, so
// tests can assert the exact TokenSet that lands in TrackerConnection.
type fakeCodeTracker struct {
	id         int
	exchangeFn func(ctx context.Context, code, verifier, redirectURI string) (tracker.TokenSet, error)
}

func (f *fakeCodeTracker) Key() string      { return "fake-code" }
func (f *fakeCodeTracker) ID() int          { return f.id }
func (f *fakeCodeTracker) Name() string     { return "Fake Code Tracker" }
func (f *fakeCodeTracker) NeedsOAuth() bool { return true }
func (f *fakeCodeTracker) AuthURL(state, _ string) (string, string, error) {
	return "https://fake.test/authorize?state=" + state, "verifier-xyz", nil
}
func (f *fakeCodeTracker) ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (tracker.TokenSet, error) {
	if f.exchangeFn != nil {
		return f.exchangeFn(ctx, code, verifier, redirectURI)
	}
	return tracker.TokenSet{Access: "access-" + code}, nil
}
func (f *fakeCodeTracker) Refresh(context.Context, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}
func (f *fakeCodeTracker) Search(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}
func (f *fakeCodeTracker) GetEntry(context.Context, string, string) (*tracker.TrackEntry, error) {
	return nil, nil
}
func (f *fakeCodeTracker) SaveEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeCodeTracker) UpdateEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeCodeTracker) DeleteEntry(context.Context, string, tracker.TrackEntry) error { return nil }

var _ tracker.Tracker = (*fakeCodeTracker)(nil)

// fakeImplicitTracker is an implicit-grant (AniList-shaped) tracker.Tracker
// test double implementing both optional capability interfaces.
type fakeImplicitTracker struct {
	id int
}

func (f *fakeImplicitTracker) Key() string      { return "fake-implicit" }
func (f *fakeImplicitTracker) ID() int          { return f.id }
func (f *fakeImplicitTracker) Name() string     { return "Fake Implicit Tracker" }
func (f *fakeImplicitTracker) NeedsOAuth() bool { return true }
func (f *fakeImplicitTracker) AuthURL(state, _ string) (string, string, error) {
	return "https://fake.test/authorize?state=" + state, "", nil
}
func (f *fakeImplicitTracker) ExchangeCode(context.Context, string, string, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrImplicitFlow
}
func (f *fakeImplicitTracker) Refresh(context.Context, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}
func (f *fakeImplicitTracker) Search(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}
func (f *fakeImplicitTracker) GetEntry(context.Context, string, string) (*tracker.TrackEntry, error) {
	return nil, nil
}
func (f *fakeImplicitTracker) SaveEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeImplicitTracker) UpdateEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeImplicitTracker) DeleteEntry(context.Context, string, tracker.TrackEntry) error {
	return nil
}
func (f *fakeImplicitTracker) TokenFromImplicit(accessToken string) (tracker.TokenSet, error) {
	if accessToken == "" {
		return tracker.TokenSet{}, fmt.Errorf("empty implicit token")
	}
	return tracker.TokenSet{Access: accessToken}, nil
}
func (f *fakeImplicitTracker) AccountInfo(context.Context, string) (tracker.AccountInfo, error) {
	return tracker.AccountInfo{RemoteUserID: "9", Username: "owner", ScoreFormat: "POINT_100"}, nil
}

var (
	_ tracker.Tracker                = (*fakeImplicitTracker)(nil)
	_ tracker.ImplicitTokenExtractor = (*fakeImplicitTracker)(nil)
	_ tracker.AccountInfoProvider    = (*fakeImplicitTracker)(nil)
)

// stateFromAuthURL extracts the "state" query parameter AuthURL embedded in
// its returned authorize URL — tests use this to build a realistic callback
// URL for CompleteOAuth without reaching into the service's internals.
func stateFromAuthURL(t *testing.T, authURL string) string {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL %q: %v", authURL, err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("authURL %q carries no state", authURL)
	}
	return state
}

const (
	fakeCodeID     = 101
	fakeImplicitID = 102
)

func newTestService(t *testing.T, client *ent.Client, publicURL string) (*connect.Service, *fakeCodeTracker, *fakeImplicitTracker) {
	t.Helper()
	code := &fakeCodeTracker{id: fakeCodeID}
	implicit := &fakeImplicitTracker{id: fakeImplicitID}
	reg := tracker.NewRegistry(code, implicit)
	return connect.NewService(client, reg, publicURL), code, implicit
}

// TestAuthURL_UnknownTracker confirms AuthURL fails closed for a trackerID
// the registry doesn't know.
func TestAuthURL_UnknownTracker(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	if _, err := svc.AuthURL(9999); !errors.Is(err, connect.ErrUnknownTracker) {
		t.Fatalf("AuthURL(9999): err = %v, want connect.ErrUnknownTracker", err)
	}
}

// TestAuthURL_PublicURLNotConfigured confirms AuthURL fails closed when the
// instance has no public URL configured — the whole subsystem stays
// dormant per spec §2 rather than building a redirect_uri of "".
func TestAuthURL_PublicURLNotConfigured(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "")

	if _, err := svc.AuthURL(fakeCodeID); !errors.Is(err, connect.ErrPublicURLNotConfigured) {
		t.Fatalf("AuthURL with no public URL: err = %v, want connect.ErrPublicURLNotConfigured", err)
	}
}

// TestCompleteOAuth_CodeFlow_CreatesConnection drives the full auth-code
// round-trip (AuthURL → callback → CompleteOAuth) and asserts a
// TrackerConnection row lands with the exchanged access token.
func TestCompleteOAuth_CodeFlow_CreatesConnection(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	authURL, err := svc.AuthURL(fakeCodeID)
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	state := stateFromAuthURL(t, authURL)

	callback := "https://tsundoku.example/auth/tracker/callback?code=abc123&state=" + state
	if err := svc.CompleteOAuth(ctx, fakeCodeID, callback); err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}

	row, err := client.TrackerConnection.Query().
		Where(entrackerconnection.TrackerID(fakeCodeID)).
		Only(ctx)
	if err != nil {
		t.Fatalf("query TrackerConnection: %v", err)
	}
	if row.AccessToken != "access-abc123" {
		t.Fatalf("row.AccessToken = %q, want access-abc123", row.AccessToken)
	}
}

// TestCompleteOAuth_ImplicitFlow_CreatesConnectionWithAccountInfo drives the
// implicit-grant round-trip and asserts the row carries the access token
// (extracted by TokenFromImplicit from the callback's access_token query
// param) PLUS the captured username/score-format (AccountInfoProvider).
func TestCompleteOAuth_ImplicitFlow_CreatesConnectionWithAccountInfo(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	authURL, err := svc.AuthURL(fakeImplicitID)
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	state := stateFromAuthURL(t, authURL)

	// The SPA is expected to turn the browser's fragment into a query
	// param before posting the callback URL here — see
	// tracker.ImplicitTokenExtractor's doc comment.
	callback := "https://tsundoku.example/auth/tracker/callback?access_token=frag-token-xyz&state=" + state
	if err := svc.CompleteOAuth(ctx, fakeImplicitID, callback); err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}

	row, err := client.TrackerConnection.Query().
		Where(entrackerconnection.TrackerID(fakeImplicitID)).
		Only(ctx)
	if err != nil {
		t.Fatalf("query TrackerConnection: %v", err)
	}
	if row.AccessToken != "frag-token-xyz" {
		t.Fatalf("row.AccessToken = %q, want frag-token-xyz", row.AccessToken)
	}
	if row.Username != "owner" || row.ScoreFormat != "POINT_100" {
		t.Fatalf("row username/score_format = %q/%q, want owner/POINT_100", row.Username, row.ScoreFormat)
	}
}

// TestCompleteOAuth_InvalidState confirms a callback whose state was never
// stashed (or already consumed) is rejected.
func TestCompleteOAuth_InvalidState(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	callback := "https://tsundoku.example/auth/tracker/callback?code=abc&state=never-stashed"
	if err := svc.CompleteOAuth(context.Background(), fakeCodeID, callback); !errors.Is(err, connect.ErrInvalidState) {
		t.Fatalf("CompleteOAuth with an unknown state: err = %v, want connect.ErrInvalidState", err)
	}
}

// TestCompleteOAuth_StateTrackerMismatch confirms a state stashed for one
// tracker cannot be replayed against a DIFFERENT tracker's callback route —
// the CSRF-correlation check failing closed even with a technically-valid
// (but wrong-tracker) state.
func TestCompleteOAuth_StateTrackerMismatch(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	authURL, err := svc.AuthURL(fakeCodeID)
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	state := stateFromAuthURL(t, authURL)

	callback := "https://tsundoku.example/auth/tracker/callback?access_token=x&state=" + state
	if err := svc.CompleteOAuth(context.Background(), fakeImplicitID, callback); !errors.Is(err, connect.ErrInvalidState) {
		t.Fatalf("CompleteOAuth with a cross-tracker state: err = %v, want connect.ErrInvalidState", err)
	}
}

// TestCompleteOAuth_ReplayFails confirms a state can only complete ONE
// login — a second CompleteOAuth call with the same callback fails, since
// the pending stash consumes the state on first use.
func TestCompleteOAuth_ReplayFails(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	authURL, err := svc.AuthURL(fakeCodeID)
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	state := stateFromAuthURL(t, authURL)
	callback := "https://tsundoku.example/auth/tracker/callback?code=abc&state=" + state

	if err := svc.CompleteOAuth(ctx, fakeCodeID, callback); err != nil {
		t.Fatalf("first CompleteOAuth: %v", err)
	}
	if err := svc.CompleteOAuth(ctx, fakeCodeID, callback); !errors.Is(err, connect.ErrInvalidState) {
		t.Fatalf("replayed CompleteOAuth: err = %v, want connect.ErrInvalidState", err)
	}
}

// TestCompleteOAuth_MissingCode confirms a code-flow callback with no code
// query parameter is rejected.
func TestCompleteOAuth_MissingCode(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	authURL, err := svc.AuthURL(fakeCodeID)
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	state := stateFromAuthURL(t, authURL)

	callback := "https://tsundoku.example/auth/tracker/callback?state=" + state
	if err := svc.CompleteOAuth(context.Background(), fakeCodeID, callback); !errors.Is(err, connect.ErrMissingCode) {
		t.Fatalf("CompleteOAuth with no code: err = %v, want connect.ErrMissingCode", err)
	}
}

// TestCompleteOAuth_MissingToken mirrors TestCompleteOAuth_MissingCode for
// the implicit flow's access_token parameter.
func TestCompleteOAuth_MissingToken(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	authURL, err := svc.AuthURL(fakeImplicitID)
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	state := stateFromAuthURL(t, authURL)

	callback := "https://tsundoku.example/auth/tracker/callback?state=" + state
	if err := svc.CompleteOAuth(context.Background(), fakeImplicitID, callback); !errors.Is(err, connect.ErrMissingToken) {
		t.Fatalf("CompleteOAuth with no access_token: err = %v, want connect.ErrMissingToken", err)
	}
}

// TestUpsertConnection_SecondLoginUpdatesRow confirms a second login for the
// SAME tracker overwrites the existing TrackerConnection row's token rather
// than creating a duplicate — exactly one row per tracker_id survives.
func TestUpsertConnection_SecondLoginUpdatesRow(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	for _, code := range []string{"first-code", "second-code"} {
		authURL, err := svc.AuthURL(fakeCodeID)
		if err != nil {
			t.Fatalf("AuthURL: %v", err)
		}
		state := stateFromAuthURL(t, authURL)
		callback := "https://tsundoku.example/auth/tracker/callback?code=" + code + "&state=" + state
		if err := svc.CompleteOAuth(ctx, fakeCodeID, callback); err != nil {
			t.Fatalf("CompleteOAuth(%s): %v", code, err)
		}
	}

	rows, err := client.TrackerConnection.Query().
		Where(entrackerconnection.TrackerID(fakeCodeID)).
		All(ctx)
	if err != nil {
		t.Fatalf("query TrackerConnection: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1 (second login must UPDATE, not duplicate)", len(rows))
	}
	if rows[0].AccessToken != "access-second-code" {
		t.Fatalf("row.AccessToken = %q, want access-second-code (the SECOND login's token)", rows[0].AccessToken)
	}
}

// TestLogout_DeletesConnection confirms Logout removes the row, and that
// logging out again (nothing left to delete) is a no-op, not an error.
func TestLogout_DeletesConnection(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	authURL, err := svc.AuthURL(fakeCodeID)
	if err != nil {
		t.Fatalf("AuthURL: %v", err)
	}
	state := stateFromAuthURL(t, authURL)
	callback := "https://tsundoku.example/auth/tracker/callback?code=abc&state=" + state
	if err := svc.CompleteOAuth(ctx, fakeCodeID, callback); err != nil {
		t.Fatalf("CompleteOAuth: %v", err)
	}

	if err := svc.Logout(ctx, fakeCodeID); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	count, err := client.TrackerConnection.Query().
		Where(entrackerconnection.TrackerID(fakeCodeID)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count TrackerConnection: %v", err)
	}
	if count != 0 {
		t.Fatalf("row count after Logout = %d, want 0", count)
	}

	// Idempotent: logging out an already-disconnected tracker is a no-op.
	if err := svc.Logout(ctx, fakeCodeID); err != nil {
		t.Fatalf("second Logout: %v", err)
	}
}

// TestLogout_UnknownTracker confirms Logout also fails closed for an
// unregistered trackerID.
func TestLogout_UnknownTracker(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	if err := svc.Logout(context.Background(), 9999); !errors.Is(err, connect.ErrUnknownTracker) {
		t.Fatalf("Logout(9999): err = %v, want connect.ErrUnknownTracker", err)
	}
}

// fakeCredentialTracker is a Kitsu/MangaUpdates-shaped tracker.Tracker test
// double: NeedsOAuth() is false, AuthURL/ExchangeCode fail closed with
// tracker.ErrOAuthNotSupported (mirroring the real kitsu/mangaupdates
// clients), and LoginCredentials succeeds deterministically from the
// username it is given, so tests can assert the exact TokenSet that lands
// in TrackerConnection.
type fakeCredentialTracker struct {
	id      int
	loginFn func(ctx context.Context, username, password string) (tracker.TokenSet, error)
}

func (f *fakeCredentialTracker) Key() string      { return "fake-credential" }
func (f *fakeCredentialTracker) ID() int          { return f.id }
func (f *fakeCredentialTracker) Name() string     { return "Fake Credential Tracker" }
func (f *fakeCredentialTracker) NeedsOAuth() bool { return false }
func (f *fakeCredentialTracker) AuthURL(string, string) (string, string, error) {
	return "", "", tracker.ErrOAuthNotSupported
}
func (f *fakeCredentialTracker) ExchangeCode(context.Context, string, string, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrOAuthNotSupported
}
func (f *fakeCredentialTracker) Refresh(context.Context, string) (tracker.TokenSet, error) {
	return tracker.TokenSet{}, tracker.ErrNoRefresh
}
func (f *fakeCredentialTracker) LoginCredentials(ctx context.Context, username, password string) (tracker.TokenSet, error) {
	if f.loginFn != nil {
		return f.loginFn(ctx, username, password)
	}
	return tracker.TokenSet{Access: "access-" + username}, nil
}
func (f *fakeCredentialTracker) Search(context.Context, string, string) ([]tracker.TrackSearchResult, error) {
	return nil, nil
}
func (f *fakeCredentialTracker) GetEntry(context.Context, string, string) (*tracker.TrackEntry, error) {
	return nil, nil
}
func (f *fakeCredentialTracker) SaveEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeCredentialTracker) UpdateEntry(_ context.Context, _ string, e tracker.TrackEntry) (tracker.TrackEntry, error) {
	return e, nil
}
func (f *fakeCredentialTracker) DeleteEntry(context.Context, string, tracker.TrackEntry) error {
	return nil
}

var (
	_ tracker.Tracker         = (*fakeCredentialTracker)(nil)
	_ tracker.CredentialLogin = (*fakeCredentialTracker)(nil)
)

const fakeCredentialID = 103

// TestLoginCredentials_CreatesConnection drives the credential-login flow
// end-to-end and asserts the exchanged TokenSet plus the owner-typed
// username land in TrackerConnection — the SAME store CompleteOAuth writes.
func TestLoginCredentials_CreatesConnection(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	reg := tracker.NewRegistry(&fakeCredentialTracker{id: fakeCredentialID})
	svc := connect.NewService(client, reg, "https://tsundoku.example")

	if err := svc.LoginCredentials(ctx, fakeCredentialID, "owner@example.test", "hunter2"); err != nil {
		t.Fatalf("LoginCredentials: %v", err)
	}

	row, err := client.TrackerConnection.Query().
		Where(entrackerconnection.TrackerID(fakeCredentialID)).
		Only(ctx)
	if err != nil {
		t.Fatalf("query TrackerConnection: %v", err)
	}
	if row.AccessToken != "access-owner@example.test" {
		t.Fatalf("row.AccessToken = %q, want access-owner@example.test", row.AccessToken)
	}
	if row.Username != "owner@example.test" {
		t.Fatalf("row.Username = %q, want the owner-typed username", row.Username)
	}
}

// TestLoginCredentials_UnknownTracker confirms LoginCredentials fails
// closed for a trackerID the registry doesn't know.
func TestLoginCredentials_UnknownTracker(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	if err := svc.LoginCredentials(context.Background(), 9999, "u", "p"); !errors.Is(err, connect.ErrUnknownTracker) {
		t.Fatalf("LoginCredentials(9999): err = %v, want connect.ErrUnknownTracker", err)
	}
}

// TestLoginCredentials_OAuthTrackerNotSupported confirms LoginCredentials
// refuses an OAuth-only tracker (one that does not implement
// tracker.CredentialLogin) rather than silently no-op-ing or panicking on
// a failed type assertion.
func TestLoginCredentials_OAuthTrackerNotSupported(t *testing.T) {
	client := testdb.New(t)
	svc, _, _ := newTestService(t, client, "https://tsundoku.example")

	err := svc.LoginCredentials(context.Background(), fakeCodeID, "u", "p")
	if !errors.Is(err, connect.ErrCredentialLoginNotSupported) {
		t.Fatalf("LoginCredentials on an OAuth tracker: err = %v, want connect.ErrCredentialLoginNotSupported", err)
	}
}

// TestLoginCredentials_PropagatesTrackerError confirms a failed credential
// exchange (bad password) surfaces as an error rather than a false-success
// TrackerConnection row.
func TestLoginCredentials_PropagatesTrackerError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	reg := tracker.NewRegistry(&fakeCredentialTracker{
		id: fakeCredentialID,
		loginFn: func(context.Context, string, string) (tracker.TokenSet, error) {
			return tracker.TokenSet{}, fmt.Errorf("invalid credentials")
		},
	})
	svc := connect.NewService(client, reg, "https://tsundoku.example")

	if err := svc.LoginCredentials(ctx, fakeCredentialID, "owner", "wrong"); err == nil {
		t.Fatalf("LoginCredentials with a failing tracker: want an error, got nil")
	}

	count, err := client.TrackerConnection.Query().
		Where(entrackerconnection.TrackerID(fakeCredentialID)).
		Count(ctx)
	if err != nil {
		t.Fatalf("count TrackerConnection: %v", err)
	}
	if count != 0 {
		t.Fatalf("TrackerConnection row count after a failed login = %d, want 0", count)
	}
}

// TestLoginCredentials_SecondLoginUpdatesRow mirrors
// TestUpsertConnection_SecondLoginUpdatesRow for the credential-login path.
func TestLoginCredentials_SecondLoginUpdatesRow(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	reg := tracker.NewRegistry(&fakeCredentialTracker{id: fakeCredentialID})
	svc := connect.NewService(client, reg, "https://tsundoku.example")

	if err := svc.LoginCredentials(ctx, fakeCredentialID, "first-user", "p1"); err != nil {
		t.Fatalf("first LoginCredentials: %v", err)
	}
	if err := svc.LoginCredentials(ctx, fakeCredentialID, "second-user", "p2"); err != nil {
		t.Fatalf("second LoginCredentials: %v", err)
	}

	rows, err := client.TrackerConnection.Query().
		Where(entrackerconnection.TrackerID(fakeCredentialID)).
		All(ctx)
	if err != nil {
		t.Fatalf("query TrackerConnection: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1 (second login must UPDATE, not duplicate)", len(rows))
	}
	if rows[0].Username != "second-user" {
		t.Fatalf("row.Username = %q, want second-user (the SECOND login)", rows[0].Username)
	}
}
