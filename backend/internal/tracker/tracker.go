// Package tracker defines the provider-agnostic contracts for the native
// tracker subsystem (AniList, MAL, Kitsu, MangaUpdates): OAuth/credential
// connect, per-series search, and reading-progress read/write. This mirrors
// internal/metadata's Provider-port shape (internal/metadata/provider.go)
// but is a SEPARATE subsystem — trackers are login+sync (Suwayomi/Komikku
// model), metadata providers are public-read (Komf model). See
// spec/trackers-and-rich-library-umbrella-v2 §1.
//
// This package is deliberately ENT-FREE (only stdlib + context/net/http): it
// holds no *ent.Client and imports nothing under internal/ent. A concrete
// Tracker implementation (internal/tracker/anilist, internal/tracker/mal)
// imports this package for the Tracker contract; a service that needs to
// persist a TokenSet against the TrackerConnection/TrackBinding schema (e.g.
// internal/tracker/connect) lives in ITS OWN package that imports both this
// package and internal/ent — never the reverse. This is the exact shape
// internal/metadata/providers documents for the same reason (breaking a
// would-be provider→port→provider import cycle).
package tracker

import (
	"context"
	"errors"
	"time"
)

// Registry ids — the STABLE numeric identity of each tracker, shared with
// ent/schema/trackerconnection.go's tracker_id / trackbinding.go's
// tracker_id columns. These mirror the Mihon/Suwayomi tracker registry so
// the ids are conventional, not invented: MAL=1, AniList=2, Kitsu=3,
// MangaUpdates=7. NEVER renumber — a stored TrackerConnection/TrackBinding
// row's tracker_id would silently point at the wrong tracker.
const (
	IDMAL          = 1
	IDAniList      = 2
	IDKitsu        = 3 // internal/tracker/kitsu (slice 3b).
	IDMangaUpdates = 7 // internal/tracker/mangaupdates (slice 3b).
)

// Sentinel errors every Tracker implementation returns for the same
// condition, so callers (the connect/bind services, the HTTP handlers) can
// branch on errors.Is regardless of which tracker raised them.
var (
	// ErrNoRefresh is returned by Refresh on a tracker whose OAuth grant has
	// no refresh token (AniList's implicit flow) — the only recovery is a
	// fresh AuthURL/CompleteOAuth round-trip (re-login).
	ErrNoRefresh = errors.New("tracker: this tracker does not support token refresh")
	// ErrTokenExpired is returned by the shared auth RoundTripper (see
	// roundtripper.go) when a request 401s and either no refresh is
	// possible or the refresh itself fails — the caller must force a
	// re-login rather than retry.
	ErrTokenExpired = errors.New("tracker: token expired and could not be refreshed")
	// ErrImplicitFlow is returned by ExchangeCode on a tracker that uses the
	// OAuth IMPLICIT grant (AniList): there is no server-exchangeable code,
	// only a fragment-delivered access token the caller must supply via
	// ImplicitTokenExtractor.TokenFromImplicit instead.
	ErrImplicitFlow = errors.New("tracker: this tracker uses the OAuth implicit flow; call TokenFromImplicit, not ExchangeCode")
	// ErrClientIDNotConfigured is returned by AuthURL when the tracker's
	// app client-id is blank — the "blank disables this tracker" pattern
	// (mirrors SuwayomiConfig.ExternalURL): a dormant/unconfigured tracker
	// fails closed rather than emitting an authorize URL that can never
	// exchange.
	ErrClientIDNotConfigured = errors.New("tracker: client id is not configured")
	// ErrOAuthNotSupported is returned by AuthURL/ExchangeCode on a tracker
	// that connects via a direct username/password or session login
	// instead of an OAuth redirect (Kitsu, MangaUpdates — NeedsOAuth() ==
	// false): there is no authorize URL or authorization code to exchange
	// for such a tracker; the caller must use CredentialLogin instead.
	ErrOAuthNotSupported = errors.New("tracker: this tracker connects via username/password, not OAuth; call LoginCredentials")
)

// TokenSet is the OAuth/session credential for one connected tracker
// account. Refresh is "" for a tracker with no refresh token (AniList).
// ExpiresAt is nil when the tracker has no known expiry; a tracker whose
// grant does not expire in practice (or expires implicitly) still gets a
// synthetic ExpiresAt so the auth RoundTripper's expiry check is uniform —
// see anilist.Client.TokenFromImplicit.
type TokenSet struct {
	Access    string
	Refresh   string
	ExpiresAt *time.Time
}

// TrackSearchResult is one tracker's search hit for a manga — the candidate
// list an owner picks from when binding a series (internal/tracker/connect
// and the bind service, slice 3b). Type/StartDate/Score/Description are
// BEST-EFFORT enrichment fields (Search-Enrichment slice): every client
// populates whatever its own search response actually carries and leaves
// the rest at its zero value — never fabricated, never normalized across
// trackers (same "native vocabulary/scale" rule Status/TotalChapters
// already follow, spec §2).
type TrackSearchResult struct {
	RemoteID string
	Title    string
	URL      string
	CoverURL string
	// Status is the tracker's OWN native status vocabulary (e.g. AniList's
	// RELEASING/FINISHED, MAL's "currently_publishing") — never normalized
	// here (spec §2: "store native scale/codes; convert only at display").
	Status string
	// TotalChapters is the tracker's reported total chapter count; 0 =
	// unknown/ongoing.
	TotalChapters int
	// Type is the tracker's own publication-format label for the result
	// (e.g. AniList's "MANGA"/"NOVEL"/"ONE_SHOT" `format`, MAL's
	// `media_type`, Kitsu's `subtype`). "" when the tracker's search
	// response doesn't carry one (MangaUpdates' `type` when absent).
	Type string
	// StartDate is the tracker's reported publication-start year or date,
	// kept as a plain STRING (not time.Time) so every tracker's native
	// granularity survives without lossy reconstruction: AniList only gives
	// a year (FuzzyDate.year, no month/day on a search hit), MAL/Kitsu give
	// a full date, MangaUpdates gives a bare year string. "" when unknown.
	StartDate string
	// Score is the CATALOG/COMMUNITY average rating for the manga — NOT the
	// caller's own entry score (that lives on TrackEntry.Score once bound).
	// Native scale per tracker, never normalized here: AniList's
	// `averageScore` is 0-100 (kept RAW, not divided to 0-10 — the FE labels
	// this "community avg" precisely so a per-tracker scale is expected);
	// MAL's `mean` and Kitsu's `averageRating` are already ~0-10/0-100
	// respectively on their own native scales. 0 = unknown (MangaUpdates'
	// search response carries no per-item community score this port reads).
	Score float64
	// Description is the tracker's own synopsis/summary text for the result
	// (AniList's `description`, MAL's `synopsis`, Kitsu's `synopsis`,
	// MangaUpdates' `description`), verbatim — no HTML stripping/decoding
	// (display-layer concern, not this port's). "" when absent.
	Description string
}

// TrackEntry is a tracker's reading-progress record for a bound series —
// either what GetEntry reads back, or what SaveEntry/UpdateEntry writes.
// Every field is in the tracker's OWN native scale/vocabulary; this port
// never converts (spec/trackers-and-rich-library-umbrella-v2 §2/§6 — score
// and status conversion is a display-layer concern for a later slice).
type TrackEntry struct {
	// RemoteID is the tracker's manga id (AniList Media id / MAL manga id).
	RemoteID string
	// LibraryID is AniList's MediaList entry id, required to UPDATE/DELETE
	// an AniList entry (distinct from RemoteID, the media/manga id). "" for
	// MAL, which is keyed by RemoteID alone (its list-status endpoints hang
	// off the manga id directly).
	LibraryID string
	Title     string
	// Status is the tracker's native status code/string.
	Status string
	// Score is on the tracker's native scale (AniList: POINT_100, 0-100;
	// MAL: 0-10).
	Score float64
	// Progress is the furthest chapter read, as a float so a fractional
	// local chapter number survives; a tracker whose wire field is an
	// integer (both AniList and MAL) truncates at the client boundary —
	// that is a wire-shape fact, not a sync-policy decision (the actual
	// local→remote push POLICY — never-regress, max-wins, filter-
	// unparseable — is Phase 4, spec §6).
	Progress      float64
	TotalChapters int
	StartDate     *time.Time
	FinishDate    *time.Time
	Private       bool
}

// AccountInfo is the logged-in tracker account's identity, captured at
// login — used to populate TrackerConnection.username / .score_format.
type AccountInfo struct {
	RemoteUserID string
	Username     string
	// ScoreFormat is AniList's account-level score format
	// (POINT_100/POINT_10/POINT_10_DECIMAL/POINT_5/POINT_3); "" for a
	// tracker with no per-account score format (MAL, Kitsu, MangaUpdates).
	ScoreFormat string
}

// Tracker is the port every native tracker (AniList, MAL, Kitsu (3b),
// MangaUpdates (3b)) implements. A concrete client is STATELESS with respect
// to any one account: every authenticated method takes the caller's current
// access token explicitly rather than holding one — the connect/bind
// service (which DOES hold state, backed by TrackerConnection) owns
// refresh/expiry lifecycle, using the shared auth RoundTripper (see
// roundtripper.go) when it wants that handled transparently across many
// calls.
type Tracker interface {
	// Key is the tracker's stable string identity (e.g. "anilist").
	Key() string
	// ID is the tracker's numeric registry id — one of the ID* constants.
	ID() int
	// Name is the tracker's human-display name (e.g. "AniList").
	Name() string
	// NeedsOAuth reports whether this tracker connects via an OAuth
	// redirect (AniList, MAL — both true) as opposed to a direct
	// username/password or session login (Kitsu, MangaUpdates — both
	// false; such a tracker instead implements CredentialLogin).
	NeedsOAuth() bool
	// SupportsPrivate reports whether this tracker's entries can be marked
	// private on the remote account (AniList's MediaList `private` flag,
	// Kitsu's library-entries `private` flag — both true) as opposed to a
	// tracker with no such remote concept at all (MAL, MangaUpdates — both
	// false; a Bind/UpdateTrack `private` request field is silently
	// harmless-ignored for these, never an error — see bind.Service.Bind's
	// own doc comment). A required capability query (every Tracker answers
	// it, even when the answer is always false) rather than an optional
	// type-asserted interface like ImplicitTokenExtractor/CredentialLogin —
	// modeled on this same interface's NeedsOAuth, the existing
	// required-boolean-capability precedent.
	SupportsPrivate() bool

	// AuthURL builds the provider's authorize URL for a fresh login. It
	// returns the URL to send the owner's browser to, plus a PKCE code
	// verifier for trackers that use PKCE (MAL) — empty for trackers that
	// don't (AniList's implicit flow has no PKCE). Returns
	// ErrClientIDNotConfigured when this tracker's app client-id is blank
	// (fail-closed — the "blank disables this tracker" pattern).
	//
	// state/redirectURI are UNUSED by every concrete implementation as of
	// the OAuth-alignment fix: AniList's and MAL's REAL authorize endpoints
	// accept neither (confirmed against the proven reference
	// implementations Suwayomi-Server and Komikku both ship — sending
	// either produced AniList's "unsupported_grant_type" error). The two
	// parameters are kept on the interface only so a caller-side signature
	// change doesn't ripple through every fake across the tree (an
	// established INTERFACE FOOTGUN this codebase avoids elsewhere too);
	// internal/tracker/connect no longer generates a CSRF state at all —
	// it correlates a pending login by TRACKER ID instead (see that
	// package's doc comment).
	AuthURL(state, redirectURI string) (authURL string, pkceVerifier string, err error)
	// ExchangeCode exchanges an OAuth authorization code (+ PKCE verifier)
	// for a TokenSet. redirectURI is likewise UNUSED (MAL's real token
	// endpoint requires no redirect_uri — see mal.Client.ExchangeCode's
	// doc comment) and kept only for the same interface-stability reason
	// as AuthURL's. Trackers that use the OAuth IMPLICIT grant (AniList)
	// have no server-exchangeable code and return ErrImplicitFlow — such a
	// tracker instead implements ImplicitTokenExtractor.
	ExchangeCode(ctx context.Context, code, pkceVerifier, redirectURI string) (TokenSet, error)
	// Refresh mints a fresh TokenSet from a refresh token. A tracker with
	// no refresh grant (AniList) always returns ErrNoRefresh.
	Refresh(ctx context.Context, refresh string) (TokenSet, error)

	// Search returns the tracker's manga search hits for a free-text query,
	// using token for authenticated request context where the tracker's API
	// requires it.
	Search(ctx context.Context, token, query string) ([]TrackSearchResult, error)
	// GetEntry fetches the caller's own reading-progress entry for
	// remoteID. Returns (nil, nil) when the manga is not yet tracked on the
	// caller's account — that is a valid empty state, not an error.
	GetEntry(ctx context.Context, token, remoteID string) (*TrackEntry, error)
	// SaveEntry creates a new tracking entry (a bind). The returned
	// TrackEntry carries the tracker's assigned LibraryID (when the tracker
	// has one — AniList) for subsequent UpdateEntry/DeleteEntry calls.
	SaveEntry(ctx context.Context, token string, entry TrackEntry) (TrackEntry, error)
	// UpdateEntry writes progress/status/score/dates to an existing entry
	// (identified by entry.LibraryID when the tracker has one, else
	// entry.RemoteID).
	UpdateEntry(ctx context.Context, token string, entry TrackEntry) (TrackEntry, error)
	// DeleteEntry removes the tracking entry (identified the same way as
	// UpdateEntry) from the tracker's own account — a REMOTE deletion, used
	// only when the owner explicitly unbinds with deleteRemote=true.
	DeleteEntry(ctx context.Context, token string, entry TrackEntry) error
}

// ImplicitTokenExtractor is implemented by a Tracker that uses the OAuth
// IMPLICIT grant (AniList): the connect service type-asserts a Tracker to
// this interface to build a TokenSet directly from a fragment-derived
// access token the SPA already extracted client-side (browsers never send a
// URL fragment to the server — see spec §5), bypassing ExchangeCode (which
// such a tracker does not support — it returns ErrImplicitFlow).
type ImplicitTokenExtractor interface {
	TokenFromImplicit(accessToken string) (TokenSet, error)
}

// AccountInfoProvider is implemented by a Tracker that can report the
// logged-in account's identity (currently AniList, for its username +
// score-format capture at login — see spec §4). A tracker without an
// equivalent self-lookup simply does not implement this interface; the
// connect service treats its absence as "no extra account info to capture",
// never an error.
type AccountInfoProvider interface {
	AccountInfo(ctx context.Context, token string) (AccountInfo, error)
}

// CredentialLogin is implemented by a Tracker that connects via a direct
// username/password or session login rather than an OAuth redirect (Kitsu,
// MangaUpdates — NeedsOAuth() == false). The connect service type-asserts a
// Tracker to this interface for its LoginCredentials method (mirroring how
// ImplicitTokenExtractor/AccountInfoProvider are type-asserted for their own
// optional capabilities) rather than adding LoginCredentials to every
// Tracker: AniList/MAL have no username/password concept at all, so forcing
// them to implement a method that could only ever error would be a
// meaningless stub, not a real capability.
type CredentialLogin interface {
	// LoginCredentials exchanges a username+password (Kitsu's OAuth
	// password grant; MangaUpdates' session login) for a TokenSet. The
	// returned token is used exactly like an OAuth-derived one — attached
	// as a Bearer credential to every subsequent authenticated call.
	LoginCredentials(ctx context.Context, username, password string) (TokenSet, error)
}
