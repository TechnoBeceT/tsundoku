// Package account is the shared TOKEN-RESOLUTION home for every tracker
// service that needs a connected account's current, usable access token.
//
// PRE-ACTIVATION GAP THIS CLOSES: internal/tracker/bind and
// internal/tracker/syncsvc each carried their own byte-identical
// accountToken helper that returned TrackerConnection.access_token VERBATIM
// — never checking expiry, never refreshing it, and never setting
// token_expired. ~1 month after connecting, MAL's access token expires;
// every authed call then 401s forever and the UI keeps showing "connected"
// with no re-login prompt. ResolveToken is now the ONE place both packages
// call: it (1) proactively refreshes an expired token before handing it
// back — reusing the exact expiry rule internal/tracker/roundtripper.go's
// (currently unwired) auth transport already uses, tracker.TokenExpired —
// and (2) flags token_expired when a refresh cannot recover the token (no
// refresh grant stored, or the refresh call itself fails), so the owner
// sees "reconnect" instead of a silently-broken tracker.
//
// This package (like internal/tracker/bind, internal/tracker/connect, and
// internal/tracker/syncsvc before it) DOES use ent — the same "subpkg that
// CAN use ent" shape internal/tracker's own doc comment documents: an
// ent-touching orchestration layer sits ABOVE the ent-free internal/tracker
// port, never the reverse. It does NOT import bind or syncsvc — they import
// IT — so there is no cycle: bind/syncsvc → account → tracker/ent.
//
// 🔴 DELIBERATELY NOT BUILT HERE (documented, future follow-up): wiring
// internal/tracker/roundtripper.go's authRoundTripper THROUGH the Tracker
// port itself, so every concrete client transparently refreshes+retries a
// reactive 401 mid-request. That needs a Tracker port change (adding an
// http.Client seam, or similar) and is out of scope for this minimal,
// port-preserving fix. Instead: refresh PROACTIVELY here before a call is
// even made, and flag REACTIVELY (see MarkExpired) when a call still comes
// back reporting tracker.ErrTokenExpired despite looking fresh.
package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// ErrTrackerNotConnected is returned when trackerID has no TrackerConnection
// row (the owner has never logged in, or logged out).
//
// bind.ErrTrackerNotConnected / syncsvc.ErrTrackerNotConnected already exist
// as the SAME condition's sentinel in each of those packages (this package
// cannot import either without recreating the cycle they depend on it to
// avoid), so bind/syncsvc's own accountToken wrapper translates this one
// into their own before returning — the handler's existing errors.Is checks
// keep matching, unchanged.
var ErrTrackerNotConnected = errors.New("account: tracker is not connected")

// ResolveToken returns trackerID's connected account's CURRENT, usable
// access token. See the package doc comment for the full behavior; in
// short:
//
//   - No TrackerConnection row → ErrTrackerNotConnected.
//   - Stored token not expired (tracker.TokenExpired false — expires_at nil
//     or still in the future) → the stored access token, UNCHANGED. This is
//     the common path: ZERO refresh calls, zero writes. AniList's TokenSet
//     has both Refresh=="" and ExpiresAt==nil, so it ALWAYS lands here —
//     never force-expired; its only real expiry signal is a reactive 401
//     the port cannot see (see the package doc comment's follow-up note).
//   - Expired + no refresh token stored → MarkExpired (best-effort), then
//     tracker.ErrTokenExpired.
//   - Expired + a refresh token stored → calls trackerID's registered
//     Tracker.Refresh(ctx, storedRefreshToken):
//     -- success: persists the fresh TokenSet (access/refresh/expires_at,
//     token_expired=false) exactly like connect.Service.upsertConnection
//     does on a fresh login, and returns the new access token.
//     -- failure (including tracker.ErrNoRefresh / tracker.ErrTokenExpired
//     bubbling up from Refresh itself): MarkExpired, then
//     tracker.ErrTokenExpired — the owner must re-login.
func ResolveToken(ctx context.Context, client *ent.Client, registry *tracker.Registry, trackerID int) (string, error) {
	conn, err := client.TrackerConnection.Query().
		Where(enttrackerconnection.TrackerID(trackerID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "", ErrTrackerNotConnected
		}
		return "", fmt.Errorf("account: query tracker connection: %w", err)
	}

	tok := tracker.TokenSet{Access: conn.AccessToken, Refresh: conn.RefreshToken, ExpiresAt: conn.ExpiresAt}
	if !tracker.TokenExpired(tok) {
		return conn.AccessToken, nil
	}

	if tok.Refresh == "" {
		MarkExpired(ctx, client, trackerID)
		return "", tracker.ErrTokenExpired
	}

	t, ok := registry.ByID(trackerID)
	if !ok {
		// Defensive: every current caller resolves trackerID against this
		// SAME registry before ever reaching ResolveToken, so an
		// unregistered id here is unreachable in practice. Fail closed
		// (force re-login) rather than panic on a nil Tracker.
		MarkExpired(ctx, client, trackerID)
		return "", tracker.ErrTokenExpired
	}

	refreshed, err := t.Refresh(ctx, tok.Refresh)
	if err != nil {
		MarkExpired(ctx, client, trackerID)
		return "", tracker.ErrTokenExpired
	}

	if _, err := conn.Update().
		SetAccessToken(refreshed.Access).
		SetRefreshToken(refreshed.Refresh).
		SetNillableExpiresAt(refreshed.ExpiresAt).
		SetTokenExpired(false).
		Save(ctx); err != nil {
		return "", fmt.Errorf("account: persist refreshed token: %w", err)
	}
	return refreshed.Access, nil
}
