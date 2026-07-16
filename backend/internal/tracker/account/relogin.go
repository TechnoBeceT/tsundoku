package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// ErrNoStoredCredentials is returned by ReloginCredentials when trackerID's
// connection has no stored password to re-login with — an OAuth tracker (which
// never stores one) or a credential tracker connected before the password
// field existed. The caller then falls back to surfacing token_expired.
var ErrNoStoredCredentials = errors.New("account: no stored credentials to re-login with")

// ReloginCredentials recovers a fresh session token for a credential-login
// tracker (MangaUpdates) whose token 401'd, by re-running its LoginCredentials
// with the username+password stored on the TrackerConnection row, then
// persisting the fresh token back (token_expired cleared). It returns the new
// access token for the in-flight request to retry with.
//
// This is the ent-touching half of the reactive-401 re-login (the concrete
// mangaupdates.Client is ent-free and holds no credentials): it is wired into
// the client's Reauthenticator hook in cmd/tsundoku/main.go. It is bounded — a
// single re-login, no retry of its own — and on ANY failure it flags the
// connection token_expired (best-effort MarkExpired) so the owner is prompted
// to reconnect rather than the tracker silently retrying forever:
//   - no connection row → ErrTrackerNotConnected;
//   - no stored password → ErrNoStoredCredentials;
//   - tracker unregistered / not a credential-login tracker → a wrapped error;
//   - LoginCredentials itself fails (bad/rotated password) → that error.
func ReloginCredentials(ctx context.Context, client *ent.Client, registry *tracker.Registry, trackerID int) (string, error) {
	conn, err := client.TrackerConnection.Query().
		Where(enttrackerconnection.TrackerID(trackerID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", ErrTrackerNotConnected
		}
		return "", fmt.Errorf("account: query tracker connection: %w", err)
	}
	if conn.Password == "" {
		MarkExpired(ctx, client, trackerID)
		return "", ErrNoStoredCredentials
	}

	t, ok := registry.ByID(trackerID)
	if !ok {
		MarkExpired(ctx, client, trackerID)
		return "", fmt.Errorf("account: tracker %d is not registered", trackerID)
	}
	cl, ok := t.(tracker.CredentialLogin)
	if !ok {
		MarkExpired(ctx, client, trackerID)
		return "", fmt.Errorf("account: tracker %s does not support credential re-login", t.Key())
	}

	tok, err := cl.LoginCredentials(ctx, conn.Username, conn.Password)
	if err != nil {
		MarkExpired(ctx, client, trackerID)
		return "", fmt.Errorf("account: re-login %s: %w", t.Key(), err)
	}

	if _, err := conn.Update().
		SetAccessToken(tok.Access).
		SetRefreshToken(tok.Refresh).
		SetNillableExpiresAt(tok.ExpiresAt).
		SetTokenExpired(false).
		Save(ctx); err != nil {
		return "", fmt.Errorf("account: persist re-logged-in token: %w", err)
	}
	return tok.Access, nil
}
