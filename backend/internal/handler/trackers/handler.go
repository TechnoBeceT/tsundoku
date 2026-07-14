// Package trackers holds the thin HTTP handlers for the Phase-3 tracker
// subsystem (spec/trackers-oauth-phase3 §4): per-account connect (OAuth +
// credential login/logout, status list) and per-series bind (search, bind,
// unbind, refresh). Business logic lives in internal/tracker/connect (the
// per-ACCOUNT half) and internal/tracker/bind (the per-SERIES half); these
// handlers only bind/parse the request, validate it, call the relevant
// service, and render the DTO — the same bind→validate→service→DTO shape as
// handler/metadata and handler/suwayomi.
//
// The Phase-4c SYNC surface (spec/trackers-sync-phase4 §4) — UpdateTrack
// (the owner's manual tracking-sheet edit) and SyncTracking (pull + converge
// a series' whole binding set) — is served the same way, over the
// handler-local SyncService interface (satisfied by *syncsvc.Service).
//
// The Handler also holds the raw *ent.Client directly (like handler/owner)
// for two READ-ONLY listings neither service exposes as a dedicated method:
// GET /api/trackers needs every registered tracker's TrackerConnection row
// (or its absence) to report isLoggedIn/isTokenExpired/username, and
// GET /api/series/:id/tracking needs a series' whole TrackBinding set. Both
// are plain, no-N+1 reads — no service business logic is duplicated here.
package trackers

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/ent"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	enttrackbinding "github.com/technobecet/tsundoku/internal/ent/trackbinding"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/tracker"
	trackerbind "github.com/technobecet/tsundoku/internal/tracker/bind"
	trackerconnect "github.com/technobecet/tsundoku/internal/tracker/connect"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
)

// SyncService is the Phase-4c tracker SYNC surface this handler depends on —
// satisfied by *syncsvc.Service. Depending on this narrow interface (rather
// than the concrete type) keeps UpdateTrack/SyncTracking testable with a
// fake, the same discipline every other handler-local port in this codebase
// follows (e.g. series.CoverFetcher).
type SyncService interface {
	// UpdateTrack applies the owner's manual tracking-sheet edit to
	// recordID's binding — see syncsvc.Service.UpdateTrack's own doc comment.
	UpdateTrack(ctx context.Context, recordID uuid.UUID, patch syncsvc.UpdatePatch) (*ent.TrackBinding, error)
	// SyncNow pulls + converges every one of seriesID's bindings — see
	// syncsvc.Service.SyncNow's own doc comment.
	SyncNow(ctx context.Context, seriesID uuid.UUID) ([]*ent.TrackBinding, error)
}

// Handler serves the tracker connect + bind + sync HTTP endpoints.
type Handler struct {
	client     *ent.Client
	registry   *tracker.Registry
	connectSvc *trackerconnect.Service
	bindSvc    *trackerbind.Service
	syncSvc    SyncService
}

// NewHandler constructs a Handler bound to the Ent client, the tracker
// registry, and the connect/bind/sync services (all built over the SAME
// registry in main.go, so a tracker id resolves identically everywhere).
func NewHandler(client *ent.Client, registry *tracker.Registry, connectSvc *trackerconnect.Service, bindSvc *trackerbind.Service, syncSvc SyncService) *Handler {
	return &Handler{client: client, registry: registry, connectSvc: connectSvc, bindSvc: bindSvc, syncSvc: syncSvc}
}

// List handles GET /api/trackers. It reports EVERY registered tracker
// (AniList, MAL, Kitsu, MangaUpdates) — including a disabled/unconfigured
// OAuth tracker (blank client-id), never omitted — with its connect status.
// A single batch query loads every TrackerConnection row up front (no N+1
// over 4 trackers). This endpoint deliberately does NOT call AuthURL (that
// would stash a PKCE verifier per call); see AuthURL below.
func (h *Handler) List(c echo.Context) error {
	ctx := c.Request().Context()
	rows, err := h.client.TrackerConnection.Query().All(ctx)
	if err != nil {
		return err
	}
	byTrackerID := make(map[int]*ent.TrackerConnection, len(rows))
	for _, r := range rows {
		byTrackerID[r.TrackerID] = r
	}

	trackerList := h.registry.Trackers()
	out := make([]TrackerDTO, 0, len(trackerList))
	for _, t := range trackerList {
		out = append(out, toTrackerDTO(t, byTrackerID[t.ID()]))
	}
	return c.JSON(http.StatusOK, out)
}

// AuthURL handles GET /api/trackers/:id/auth-url. It builds a FRESH
// authorize URL on demand (connect.Service.AuthURL generates a random state
// and stashes any PKCE verifier server-side) — kept as its own endpoint,
// separate from List, so a plain GET /api/trackers never stashes N pending
// logins the owner never completes. Returns 404 for an unknown trackerId,
// 400 when the tracker doesn't support OAuth, its client-id isn't
// configured, or this instance's public URL isn't configured yet (see
// mapServiceError).
func (h *Handler) AuthURL(c echo.Context) error {
	trackerID, err := validateTrackerID(c.Param("id"))
	if err != nil {
		return err
	}

	authURL, err := h.connectSvc.AuthURL(trackerID)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, TrackerAuthURLDTO{AuthURL: authURL})
}

// LoginOAuth handles POST /api/trackers/:id/login/oauth. It completes the
// OAuth round-trip AuthURL started (connect.Service.CompleteOAuth) and
// returns the refreshed TrackerDTO so the owner sees the new connect status
// without a refetch (§16 round-trip).
func (h *Handler) LoginOAuth(c echo.Context) error {
	trackerID, err := validateTrackerID(c.Param("id"))
	if err != nil {
		return err
	}
	var req OAuthLoginRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	callbackURL, err := validateOAuthLogin(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.connectSvc.CompleteOAuth(ctx, trackerID, callbackURL); err != nil {
		return mapServiceError(err)
	}
	return h.renderTrackerStatus(c, trackerID)
}

// LoginCredentials handles POST /api/trackers/:id/login/credentials — a
// direct username/password login for a credential-based tracker (Kitsu,
// MangaUpdates). The password is bound straight from the request body and
// handed to the service; it is NEVER logged or echoed back anywhere in this
// handler.
func (h *Handler) LoginCredentials(c echo.Context) error {
	trackerID, err := validateTrackerID(c.Param("id"))
	if err != nil {
		return err
	}
	var req CredentialLoginRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	username, password, err := validateCredentialLogin(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	if err := h.connectSvc.LoginCredentials(ctx, trackerID, username, password); err != nil {
		return mapServiceError(err)
	}
	return h.renderTrackerStatus(c, trackerID)
}

// Logout handles POST /api/trackers/:id/logout. Idempotent (mirrors
// connect.Service.Logout): logging out an already-disconnected tracker is
// still a 204, never a 404.
func (h *Handler) Logout(c echo.Context) error {
	trackerID, err := validateTrackerID(c.Param("id"))
	if err != nil {
		return err
	}
	if err := h.connectSvc.Logout(c.Request().Context(), trackerID); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// renderTrackerStatus re-reads trackerID's TrackerConnection row and renders
// the refreshed TrackerDTO — the shared §16 round-trip tail for both login
// endpoints. trackerID is already known-registered at this point (the
// preceding connect-service call succeeded), so ByID's ok is not re-checked.
func (h *Handler) renderTrackerStatus(c echo.Context, trackerID int) error {
	t, _ := h.registry.ByID(trackerID)
	ctx := c.Request().Context()
	conn, err := h.client.TrackerConnection.Query().
		Where(enttrackerconnection.TrackerID(trackerID)).
		Only(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toTrackerDTO(t, conn))
}

// Search handles GET /api/trackers/:id/search?q= — an AUTHED search against
// the connected account (bind.Service.SearchTracker). Returns 400 for a
// missing/blank q (validated before the service is ever called), 404 for an
// unknown trackerId, and 400 when the tracker has no connected account.
func (h *Handler) Search(c echo.Context) error {
	trackerID, err := validateTrackerID(c.Param("id"))
	if err != nil {
		return err
	}
	q, err := validateQuery(c.QueryParam("q"))
	if err != nil {
		return err
	}

	results, err := h.bindSvc.SearchTracker(c.Request().Context(), trackerID, q)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, toTrackSearchResultDTOs(results))
}

// ListBindings handles GET /api/series/:id/tracking — the series' current
// tracker bindings. A plain, no-N+1 read (one existence check + one batch
// query); see the package doc comment for why this stays in the handler
// rather than a new bind.Service method.
func (h *Handler) ListBindings(c echo.Context) error {
	seriesID, err := validateUUID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()

	exists, err := h.client.Series.Query().Where(entseries.IDEQ(seriesID)).Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	}

	rows, err := h.client.TrackBinding.Query().Where(enttrackbinding.SeriesID(seriesID)).All(ctx)
	if err != nil {
		return err
	}
	scoreFormats, err := h.resolveScoreFormats(ctx, rows)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toTrackBindingDTOs(rows, h.registry, scoreFormats))
}

// CreateBinding handles POST /api/series/:id/tracking — binds the series to
// trackerId's remoteId entry (bind.Service.Bind; create-if-absent on the
// remote account) and returns the created/updated TrackBindingDTO.
func (h *Handler) CreateBinding(c echo.Context) error {
	seriesID, err := validateUUID(c.Param("id"), "series id")
	if err != nil {
		return err
	}
	var req BindRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	trackerID, remoteID, err := validateBind(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	binding, err := h.bindSvc.Bind(ctx, seriesID, trackerID, remoteID)
	if err != nil {
		return mapServiceError(err)
	}
	scoreFormat, err := h.resolveScoreFormat(ctx, binding.TrackerID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusCreated, toTrackBindingDTO(binding, h.registry, scoreFormat))
}

// DeleteBinding handles DELETE /api/series/:id/tracking/:recordId?deleteRemote=
// (bind.Service.Unbind). recordId alone identifies the TrackBinding row (its
// id is globally unique); the :id series segment is still validated for a
// precise 400 on a malformed value, matching every other nested series route.
func (h *Handler) DeleteBinding(c echo.Context) error {
	if _, err := validateUUID(c.Param("id"), "series id"); err != nil {
		return err
	}
	recordID, err := validateUUID(c.Param("recordId"), "record id")
	if err != nil {
		return err
	}
	deleteRemote, err := validateDeleteRemote(c.QueryParam("deleteRemote"))
	if err != nil {
		return err
	}

	if err := h.bindSvc.Unbind(c.Request().Context(), recordID, deleteRemote); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// RefreshBinding handles POST /api/series/:id/tracking/:recordId/refresh —
// re-pulls the remote entry (bind.Service.FetchTrack) and returns the
// refreshed TrackBindingDTO.
func (h *Handler) RefreshBinding(c echo.Context) error {
	if _, err := validateUUID(c.Param("id"), "series id"); err != nil {
		return err
	}
	recordID, err := validateUUID(c.Param("recordId"), "record id")
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	binding, err := h.bindSvc.FetchTrack(ctx, recordID)
	if err != nil {
		return mapServiceError(err)
	}
	scoreFormat, err := h.resolveScoreFormat(ctx, binding.TrackerID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toTrackBindingDTO(binding, h.registry, scoreFormat))
}

// UpdateTrack handles POST /api/series/:id/tracking/:recordId/update — the
// owner's manual tracking-sheet edit (syncsvc.Service.UpdateTrack). Unlike
// the reading-triggered push/sync paths, this is an explicit owner action:
// a failure is returned to the caller, never silently dropped (§16). Every
// patched field is pushed to the tracker's own account before being
// persisted locally; the response carries the refreshed TrackBindingDTO
// (§16 round-trip).
func (h *Handler) UpdateTrack(c echo.Context) error {
	if _, err := validateUUID(c.Param("id"), "series id"); err != nil {
		return err
	}
	recordID, err := validateUUID(c.Param("recordId"), "record id")
	if err != nil {
		return err
	}
	var req UpdateTrackRequest
	if err := c.Bind(&req); err != nil {
		return httperr.BadRequest("invalid request body")
	}
	patch, err := validateUpdateTrack(req)
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	binding, err := h.syncSvc.UpdateTrack(ctx, recordID, patch)
	if err != nil {
		return mapServiceError(err)
	}
	scoreFormat, err := h.resolveScoreFormat(ctx, binding.TrackerID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toTrackBindingDTO(binding, h.registry, scoreFormat))
}

// SyncTracking handles POST /api/series/:id/tracking/sync — pulls EVERY one
// of seriesID's TrackBinding entries from its tracker's own account and
// converges local↔remote (syncsvc.Service.SyncNow; umbrella spec §6 "max
// wins both directions"). A single binding's own sync failure is absorbed
// by SyncNow itself (logged, that binding's pre-sync row is kept unchanged)
// so this handler only ever surfaces a hard failure loading the series'
// binding set. Returns the refreshed binding set (§16 round-trip); an
// unknown seriesID is not an error — SyncNow simply finds zero bindings and
// this returns 200 + [].
func (h *Handler) SyncTracking(c echo.Context) error {
	seriesID, err := validateUUID(c.Param("id"), "series id")
	if err != nil {
		return err
	}

	ctx := c.Request().Context()
	bindings, err := h.syncSvc.SyncNow(ctx, seriesID)
	if err != nil {
		return mapServiceError(err)
	}
	scoreFormats, err := h.resolveScoreFormats(ctx, bindings)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toTrackBindingDTOs(bindings, h.registry, scoreFormats))
}

// mapServiceError translates a connect/bind/tracker sentinel error into its
// documented HTTP status:
//   - 404 — the tracker id or series/binding record does not exist
//     (connect.ErrUnknownTracker, bind.ErrTrackerNotFound,
//     bind.ErrSeriesNotFound, bind.ErrBindingNotFound, syncsvc.ErrTrackerNotFound,
//     syncsvc.ErrBindingNotFound — syncsvc's sentinels deliberately mirror
//     bind's own shape, see syncsvc's package doc comment).
//   - 400 — the request cannot succeed as shaped: no connected account
//     (bind.ErrTrackerNotConnected, syncsvc.ErrTrackerNotConnected), a
//     bad/expired/missing OAuth callback state
//     (connect.ErrInvalidState/ErrMissingCode/ErrMissingToken), this
//     instance isn't configured for OAuth yet
//     (connect.ErrPublicURLNotConfigured, tracker.ErrClientIDNotConfigured),
//     or the tracker doesn't support the flow being called
//     (connect.ErrCredentialLoginNotSupported, tracker.ErrOAuthNotSupported),
//     or the connected account's token has expired and needs a fresh login
//     (tracker.ErrTokenExpired).
//   - 502 — anything else. Every remaining connect/bind/sync method wraps
//     either a genuine tracker-client/network failure (ExchangeCode,
//     GetEntry, SaveEntry, UpdateEntry, DeleteEntry, Search) or a DB write
//     failure with fmt.Errorf, neither of which carries its own sentinel;
//     since the dominant real failure mode of these tracker-calling methods
//     is the upstream tracker being unreachable or rejecting the request,
//     an unmatched error is surfaced as a 502 via the shared httperr.Upstream
//     (mirrors the Suwayomi settings/extensions proxies: any unmatched
//     client failure is a gateway error, never a false 200 or an opaque 500).
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, trackerconnect.ErrUnknownTracker),
		errors.Is(err, trackerbind.ErrTrackerNotFound),
		errors.Is(err, trackerbind.ErrSeriesNotFound),
		errors.Is(err, trackerbind.ErrBindingNotFound),
		errors.Is(err, syncsvc.ErrTrackerNotFound),
		errors.Is(err, syncsvc.ErrBindingNotFound):
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, trackerbind.ErrTrackerNotConnected),
		errors.Is(err, syncsvc.ErrTrackerNotConnected),
		errors.Is(err, trackerconnect.ErrInvalidState),
		errors.Is(err, trackerconnect.ErrMissingCode),
		errors.Is(err, trackerconnect.ErrMissingToken),
		errors.Is(err, trackerconnect.ErrPublicURLNotConfigured),
		errors.Is(err, trackerconnect.ErrCredentialLoginNotSupported),
		errors.Is(err, tracker.ErrClientIDNotConfigured),
		errors.Is(err, tracker.ErrOAuthNotSupported),
		errors.Is(err, tracker.ErrTokenExpired):
		return httperr.BadRequest(err.Error())
	default:
		return httperr.Upstream(err)
	}
}
