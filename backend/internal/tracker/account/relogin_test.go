package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/account"
)

// seedCredentialConnection creates a TrackerConnection row carrying stored
// username+password (the credential-tracker shape ReloginCredentials reads),
// with a token already flagged expired to prove a successful re-login clears
// the flag.
func seedCredentialConnection(ctx context.Context, t *testing.T, client *ent.Client, trackerID int, username, password string) {
	t.Helper()
	if _, err := client.TrackerConnection.Create().
		SetTrackerID(trackerID).
		SetAccessToken("stale-session").
		SetUsername(username).
		SetPassword(password).
		SetTokenExpired(true).
		Save(ctx); err != nil {
		t.Fatalf("seed credential connection: %v", err)
	}
}

// TestReloginCredentials_Success confirms a re-login with the stored
// credentials mints a fresh session token, persists it (clearing
// token_expired), and returns the new access token.
func TestReloginCredentials_Success(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedCredentialConnection(ctx, t, client, fakeTrackerID, "owner", "hunter2")

	ft := &fakeTracker{
		id: fakeTrackerID,
		loginFn: func(_ context.Context, username, password string) (tracker.TokenSet, error) {
			if username != "owner" || password != "hunter2" {
				t.Fatalf("LoginCredentials got %q/%q, want owner/hunter2", username, password)
			}
			return tracker.TokenSet{Access: "fresh-session"}, nil
		},
	}
	registry := tracker.NewRegistry(ft)

	token, err := account.ReloginCredentials(ctx, client, registry, fakeTrackerID)
	if err != nil {
		t.Fatalf("ReloginCredentials: %v", err)
	}
	if token != "fresh-session" {
		t.Fatalf("ReloginCredentials = %q, want fresh-session", token)
	}
	if ft.loginCalls != 1 {
		t.Fatalf("LoginCredentials calls = %d, want 1", ft.loginCalls)
	}

	conn := reload(ctx, t, client, fakeTrackerID)
	if conn.AccessToken != "fresh-session" || conn.TokenExpired {
		t.Fatalf("persisted connection = access %q / token_expired %v, want fresh-session / false", conn.AccessToken, conn.TokenExpired)
	}
}

// TestReloginCredentials_NoStoredPassword confirms a connection with no stored
// password (an OAuth tracker, or a credential tracker connected before the
// password field existed) returns ErrNoStoredCredentials and flags the row
// expired WITHOUT ever calling LoginCredentials.
func TestReloginCredentials_NoStoredPassword(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedCredentialConnection(ctx, t, client, fakeTrackerID, "owner", "")

	ft := &fakeTracker{id: fakeTrackerID}
	registry := tracker.NewRegistry(ft)

	_, err := account.ReloginCredentials(ctx, client, registry, fakeTrackerID)
	if !errors.Is(err, account.ErrNoStoredCredentials) {
		t.Fatalf("ReloginCredentials err = %v, want account.ErrNoStoredCredentials", err)
	}
	if ft.loginCalls != 0 {
		t.Fatalf("LoginCredentials calls = %d, want 0 (no password to log in with)", ft.loginCalls)
	}
	if conn := reload(ctx, t, client, fakeTrackerID); !conn.TokenExpired {
		t.Fatalf("token_expired = false, want true (no stored credentials ⇒ flag for reconnect)")
	}
}

// TestReloginCredentials_LoginFails confirms a failed re-login surfaces the
// error and flags the connection expired, persisting no new token.
func TestReloginCredentials_LoginFails(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seedCredentialConnection(ctx, t, client, fakeTrackerID, "owner", "wrong")

	ft := &fakeTracker{
		id: fakeTrackerID,
		loginFn: func(context.Context, string, string) (tracker.TokenSet, error) {
			return tracker.TokenSet{}, errors.New("mangaupdates: login failed")
		},
	}
	registry := tracker.NewRegistry(ft)

	if _, err := account.ReloginCredentials(ctx, client, registry, fakeTrackerID); err == nil {
		t.Fatal("ReloginCredentials with a failing login: want an error, got nil")
	}
	conn := reload(ctx, t, client, fakeTrackerID)
	if !conn.TokenExpired || conn.AccessToken != "stale-session" {
		t.Fatalf("persisted connection = access %q / token_expired %v, want stale-session / true (no new token on failure)", conn.AccessToken, conn.TokenExpired)
	}
}

// TestReloginCredentials_NotConnected confirms a missing connection row fails
// closed with account.ErrTrackerNotConnected.
func TestReloginCredentials_NotConnected(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	ft := &fakeTracker{id: fakeTrackerID}
	registry := tracker.NewRegistry(ft)

	if _, err := account.ReloginCredentials(ctx, client, registry, fakeTrackerID); !errors.Is(err, account.ErrTrackerNotConnected) {
		t.Fatalf("ReloginCredentials err = %v, want account.ErrTrackerNotConnected", err)
	}
}
