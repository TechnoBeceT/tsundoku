// Package connect is the tracker CONNECT service: it builds a tracker's
// OAuth authorize URL (stashing any PKCE verifier server-side), completes
// the callback round-trip, and upserts the resulting TokenSet (+ username /
// AniList score-format) into the app-wide TrackerConnection row for that
// tracker.
//
// PENDING-LOGIN CORRELATION IS BY TRACKER ID, NOT BY AN OAUTH "state"
// PARAMETER. AniList's and MAL's REAL authorize endpoints do not accept a
// state (or redirect_uri) parameter at all — confirmed against the proven
// reference implementations Suwayomi-Server and Komikku ship
// (AnilistApi.kt/MyAnimeListApi.kt authUrl()), neither of which sends
// either. Tsundoku's earlier version invented both, and AniList answered
// with "unsupported_grant_type" — a login that could never succeed. Since
// there is no server-generated state for a callback to echo back, this
// package stashes the in-flight PKCE verifier keyed by the TRACKER ID
// itself (see pending.go): AuthURL(trackerID) and CompleteOAuth(trackerID,
// url) already both carry the tracker id end to end (the HTTP routes are
// /api/trackers/:id/auth-url and /api/trackers/:id/login/oauth), so this is
// a clean 1:1 correlation — one in-flight login per tracker, exactly
// mirroring the reference clients' own single per-tracker verifier.
//
// SECURITY NOTE (accepted trade-off): dropping the OAuth "state" parameter
// removes the classic CSRF token an authorize redirect would otherwise
// carry. This is acceptable under Tsundoku's single-owner threat model: for
// AniList's implicit grant the access token only ever lands in fragment of
// the OWNER'S OWN browser (never sent to any server, let alone an
// attacker's), and for MAL's auth-code grant PKCE (code_verifier) already
// proves the code exchange came from the same client that started the
// login — a bare "state" echo would have added no further protection here.
// The frontend additionally correlates which tracker a callback belongs to
// via its own sessionStorage before ever calling this service. This mirrors
// the reference implementations, which likewise send no state.
//
// This package (unlike internal/tracker itself) DOES use ent — it is the
// "subpkg that CAN use ent" internal/tracker's own doc comment calls out,
// keeping the ent-free port package free of *ent.Client and avoiding an
// import cycle the same way internal/tracker/providers does for the
// concrete client wiring.
package connect

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// Sentinel errors.
var (
	// ErrUnknownTracker is returned when trackerID does not match any
	// tracker in the Service's Registry.
	ErrUnknownTracker = errors.New("connect: unknown tracker id")
	// ErrInvalidState is returned when a callback names a tracker with no
	// matching pending login — never started (AuthURL not called first),
	// already consumed (a replay), or expired (pendingStashTTL elapsed).
	// Named "state" for backward compatibility with earlier CSRF-state
	// terminology, but the check itself is now purely "is there a pending
	// login for this tracker id" — see the package doc comment.
	ErrInvalidState = errors.New("connect: invalid or expired login state")
	// ErrMissingCode is returned when a callback URL for an auth-code
	// tracker (MAL) carries no "code" query parameter.
	ErrMissingCode = errors.New("connect: callback is missing the authorization code")
	// ErrMissingToken is returned when a callback URL for an implicit-flow
	// tracker (AniList) carries no "access_token" in either its query
	// string or its URL fragment (see callbackParams — the backend parses
	// both, so the SPA does not need to pre-convert the fragment itself).
	ErrMissingToken = errors.New("connect: callback is missing the access token")
	// ErrPublicURLNotConfigured is returned by AuthURL when
	// TSUNDOKU_TRACKER_PUBLICURL is blank — there is no configured instance
	// URL, so the whole subsystem stays dormant (spec §2) rather than
	// build a redirect the owner could never complete a callback against.
	ErrPublicURLNotConfigured = errors.New("connect: TSUNDOKU_TRACKER_PUBLICURL is not configured")
	// ErrCredentialLoginNotSupported is returned by LoginCredentials when
	// trackerID names a tracker that connects via OAuth (AniList, MAL) —
	// such a tracker does not implement tracker.CredentialLogin, and the
	// caller should be using AuthURL/CompleteOAuth instead.
	ErrCredentialLoginNotSupported = errors.New("connect: this tracker connects via OAuth, not username/password")
)

// Service is the tracker connect service.
type Service struct {
	client    *ent.Client
	registry  *tracker.Registry
	publicURL string
	stash     *pendingStash
}

// NewService builds a Service. publicURL is this instance's own public base
// URL (config.TrackerConfig.PublicURL) — its only remaining role is the
// AuthURL fail-closed gate below (spec §2: the whole OAuth subsystem stays
// dormant until an instance URL is configured); it is no longer combined
// into a redirect_uri sent to any provider (see the package doc comment).
// Trailing slashes are trimmed for a stable ErrPublicURLNotConfigured check.
func NewService(client *ent.Client, registry *tracker.Registry, publicURL string) *Service {
	return &Service{
		client:    client,
		registry:  registry,
		publicURL: strings.TrimRight(publicURL, "/"),
		stash:     newPendingStash(),
	}
}

// AuthURL builds trackerID's authorize URL for a fresh login: it stashes
// any PKCE verifier the tracker's own AuthURL produced in s.stash (an
// instance field, never a package-level var; see pendingStash's doc
// comment), keyed by trackerID itself (see the package doc comment for why
// there is no CSRF state to key by instead), and returns the authorize URL
// to send the owner's browser to. state/redirectURI are passed as "" — no
// real tracker's authorize endpoint accepts either (see anilist/mal's own
// AuthURL doc comments).
//
// Returns ErrUnknownTracker for an unregistered trackerID,
// ErrPublicURLNotConfigured when this instance has no public URL set, and
// whatever the tracker's own AuthURL returns (e.g.
// tracker.ErrClientIDNotConfigured) when that tracker's app client-id is
// blank.
func (s *Service) AuthURL(trackerID int) (string, error) {
	t, ok := s.registry.ByID(trackerID)
	if !ok {
		return "", ErrUnknownTracker
	}
	if s.publicURL == "" {
		return "", ErrPublicURLNotConfigured
	}

	authURL, verifier, err := t.AuthURL("", "")
	if err != nil {
		return "", err
	}

	s.stash.Put(trackerID, pendingLogin{PKCEVerifier: verifier})
	return authURL, nil
}

// CompleteOAuth finishes the login trackerID's AuthURL started: it parses
// callbackURL's parameters (see callbackParams — query AND fragment, both),
// looks up the login pending FOR THIS TRACKER ID (consuming it — a callback
// can only be completed once; see the package doc comment for why this is
// no longer a state lookup), exchanges the code/token for a TokenSet (via
// ExchangeCode for an auth-code tracker, or TokenFromImplicit for an
// implicit-flow one — see exchangeToken), best-effort captures the
// account's username/score-format when the tracker supports it, and upserts
// the result into that tracker's TrackerConnection row.
//
// callbackURL is expected to be a real URL carrying either "code" (MAL —
// delivered in the query string) or "access_token" (AniList's implicit
// grant — delivered in the URL FRAGMENT). The frontend forwards the
// browser's full `window.location.href` verbatim (fragment intact) — it
// does NOT pre-convert the fragment into a query parameter — so this is the
// one and only place both shapes are read; see callbackParams.
func (s *Service) CompleteOAuth(ctx context.Context, trackerID int, callbackURL string) error {
	t, ok := s.registry.ByID(trackerID)
	if !ok {
		return ErrUnknownTracker
	}

	u, err := url.Parse(callbackURL)
	if err != nil {
		return fmt.Errorf("connect: invalid callback url: %w", err)
	}
	params := callbackParams(u)

	pending, ok := s.stash.Take(trackerID)
	if !ok {
		return ErrInvalidState
	}

	tok, err := s.exchangeToken(ctx, t, params, pending)
	if err != nil {
		return err
	}

	username, scoreFormat := s.lookupAccountInfo(ctx, t, tok)
	// An OAuth login stores no password (recovery is a refresh grant or a fresh
	// redirect, never a stored credential).
	return s.upsertConnection(ctx, trackerID, tok, connectionMeta{username: username, scoreFormat: scoreFormat})
}

// callbackParams returns the merged OAuth callback parameters for u: the
// standard query string, topped up with whatever u's URL FRAGMENT carries
// for any key the query doesn't already have. AniList's implicit grant
// delivers "access_token" in the fragment (browsers never send a fragment
// to a server on a normal request — a server-side url.Parse still sees it
// here only because the frontend read window.location.href client-side and
// posted the whole string in the request body); MAL's auth-code flow puts
// everything in the query and carries no fragment at all, so its behavior
// is unchanged by this merge. Precedence: a value already present in the
// query always wins over the same key in the fragment — the query is
// queried first and never overwritten — though in practice a real callback
// URL only ever populates one or the other, never both, for a given key.
func callbackParams(u *url.URL) url.Values {
	params := u.Query()
	if u.Fragment == "" {
		return params
	}
	frag, err := url.ParseQuery(u.Fragment)
	if err != nil {
		// A malformed fragment is treated as "no fragment params" rather
		// than failing the whole callback — the query alone (e.g. a bare
		// "state" on its own) still gets a chance to be valid, and an
		// invalid/missing required param surfaces via the normal
		// ErrInvalidState/ErrMissingCode/ErrMissingToken checks below.
		return params
	}
	for key, values := range frag {
		if _, exists := params[key]; !exists {
			params[key] = values
		}
	}
	return params
}

// exchangeToken turns the callback's merged query+fragment parameters (see
// callbackParams) into a TokenSet, branching on whether t uses the OAuth
// implicit grant (AniList — reads access_token, never calls ExchangeCode)
// or auth-code (MAL — reads code, calls ExchangeCode with pending's
// stashed PKCE verifier). redirectURI is passed as "" — MAL's real token
// endpoint doesn't require one (see mal.Client.ExchangeCode's doc comment).
func (s *Service) exchangeToken(ctx context.Context, t tracker.Tracker, q url.Values, pending pendingLogin) (tracker.TokenSet, error) {
	if impl, ok := t.(tracker.ImplicitTokenExtractor); ok {
		accessToken := q.Get("access_token")
		if accessToken == "" {
			return tracker.TokenSet{}, ErrMissingToken
		}
		tok, err := impl.TokenFromImplicit(accessToken)
		if err != nil {
			return tracker.TokenSet{}, tracker.WrapUpstream(t.Key(), err)
		}
		return tok, nil
	}

	code := q.Get("code")
	if code == "" {
		return tracker.TokenSet{}, ErrMissingCode
	}
	tok, err := t.ExchangeCode(ctx, code, pending.PKCEVerifier, "")
	if err != nil {
		return tracker.TokenSet{}, tracker.WrapUpstream(t.Key(), err)
	}
	return tok, nil
}

// lookupAccountInfo best-effort captures the account's username/score
// format when t implements tracker.AccountInfoProvider (currently only
// AniList). A lookup failure — or t simply not supporting it — never fails
// the whole login: the TokenSet is already good, and username/score-format
// are display niceties, not required for a working connection.
func (s *Service) lookupAccountInfo(ctx context.Context, t tracker.Tracker, tok tracker.TokenSet) (username, scoreFormat string) {
	ai, ok := t.(tracker.AccountInfoProvider)
	if !ok {
		return "", ""
	}
	info, err := ai.AccountInfo(ctx, tok.Access)
	if err != nil {
		return "", ""
	}
	return info.Username, info.ScoreFormat
}

// LoginCredentials completes a direct username/password login for a
// credential-based tracker (Kitsu, MangaUpdates — NeedsOAuth() == false):
// it type-asserts the tracker to tracker.CredentialLogin (returning
// ErrCredentialLoginNotSupported for an OAuth tracker, which does not
// implement it), exchanges the credentials for a TokenSet, and upserts the
// result into that tracker's TrackerConnection row — the SAME store
// CompleteOAuth writes, so a bind/fetch caller never needs to know which
// login flow produced a given account's token. username is stored verbatim
// as the connection's display username (mirrors CompleteOAuth's
// lookupAccountInfo capture, but here the owner-typed username is already
// known — no extra self-lookup call is needed).
//
// Returns ErrUnknownTracker for an unregistered trackerID and whatever the
// tracker's own LoginCredentials returns (e.g. a wrapped 401 on bad
// credentials) on failure.
func (s *Service) LoginCredentials(ctx context.Context, trackerID int, username, password string) error {
	t, ok := s.registry.ByID(trackerID)
	if !ok {
		return ErrUnknownTracker
	}
	cl, ok := t.(tracker.CredentialLogin)
	if !ok {
		return ErrCredentialLoginNotSupported
	}

	tok, err := cl.LoginCredentials(ctx, username, password)
	if err != nil {
		return tracker.WrapUpstream(t.Key(), fmt.Errorf("connect: %s credential login: %w", t.Key(), err))
	}

	// Store the password (only for a credential tracker) so a later reactive-401
	// re-login can recover a dead session without a manual reconnect — see
	// TrackerConnection.password's schema doc + account.ReloginCredentials.
	return s.upsertConnection(ctx, trackerID, tok, connectionMeta{username: username, password: password})
}

// Logout deletes trackerID's TrackerConnection row, discarding its stored
// token — the owner must re-run AuthURL/CompleteOAuth to reconnect.
// Idempotent: logging out an already-disconnected tracker deletes zero rows
// and returns nil, never an error.
func (s *Service) Logout(ctx context.Context, trackerID int) error {
	if _, ok := s.registry.ByID(trackerID); !ok {
		return ErrUnknownTracker
	}
	if _, err := s.client.TrackerConnection.Delete().
		Where(trackerconnection.TrackerID(trackerID)).
		Exec(ctx); err != nil {
		return fmt.Errorf("connect: delete tracker connection: %w", err)
	}
	return nil
}

// connectionMeta carries the non-token account fields upsertConnection
// persists alongside a TokenSet: the display username, the AniList
// score-format (OAuth only), and the credential password (credential-login
// trackers only — "" everywhere else). Grouped into one struct so
// upsertConnection keeps a small parameter list as its inputs grew.
type connectionMeta struct {
	username    string
	scoreFormat string
	password    string
}

// upsertConnection writes tok (+ meta) into trackerID's TrackerConnection row,
// creating it on first login and overwriting the prior token set on every
// subsequent one — a query-then-create/update pattern (mirrors the codebase's
// other find-or-create call sites, e.g. category.FindOrCreate) since
// tracker_id has no upsert-on-conflict clause wired here. token_expired is
// explicitly cleared on every successful login, since a fresh TokenSet is by
// definition not expired.
func (s *Service) upsertConnection(ctx context.Context, trackerID int, tok tracker.TokenSet, meta connectionMeta) error {
	existing, err := s.client.TrackerConnection.Query().
		Where(trackerconnection.TrackerID(trackerID)).
		Only(ctx)

	switch {
	case ent.IsNotFound(err):
		if _, cerr := s.client.TrackerConnection.Create().
			SetTrackerID(trackerID).
			SetAccessToken(tok.Access).
			SetRefreshToken(tok.Refresh).
			SetNillableExpiresAt(tok.ExpiresAt).
			SetUsername(meta.username).
			SetPassword(meta.password).
			SetScoreFormat(meta.scoreFormat).
			Save(ctx); cerr != nil {
			return fmt.Errorf("connect: create tracker connection: %w", cerr)
		}
		return nil
	case err != nil:
		return fmt.Errorf("connect: query tracker connection: %w", err)
	default:
		if _, uerr := existing.Update().
			SetAccessToken(tok.Access).
			SetRefreshToken(tok.Refresh).
			SetNillableExpiresAt(tok.ExpiresAt).
			SetUsername(meta.username).
			SetPassword(meta.password).
			SetScoreFormat(meta.scoreFormat).
			SetTokenExpired(false).
			Save(ctx); uerr != nil {
			return fmt.Errorf("connect: update tracker connection: %w", uerr)
		}
		return nil
	}
}
