// Package library contains the thin HTTP handlers for the on-disk
// library-import workflow: scan storage, list staged imports, search a
// staged entry's title across Suwayomi sources, import a staged entry
// without re-downloading, and attach an additional source to an existing
// series. Business logic lives in internal/library (the service); these
// handlers only bind, validate, call the service, and render the DTO.
//
// The service package internal/library shares this package's name — see
// validate.go / this file for the unaliased "library" import (no conflict:
// a file's own package clause does not introduce an identifier); routes.go
// aliases the HANDLER import (libraryh) instead, since it also needs the
// service package unaliased.
package library

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/handler/sourcefilter"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
)

// Handler holds the dependencies for the library-import HTTP handlers. All
// business logic lives in the library.Service; the handler is thin.
type Handler struct {
	svc *library.Service
}

// NewHandler constructs a Handler bound to a library.Service.
func NewHandler(svc *library.Service) *Handler {
	return &Handler{svc: svc}
}

// scanStartedResponse is the wire shape returned by POST /api/library/scan:
// {"started":true} on 202 once the async scan is launched, or
// {"started":false} on 409 when one was already in flight.
type scanStartedResponse struct {
	Started bool `json:"started"`
}

// Scan handles POST /api/library/scan.
//
// It launches an async walk of the storage root (library.Service.StartScan),
// streaming scan.start/scan.progress/scan.done over the existing SSE hub as
// it upserts one ImportEntry per on-disk series (never downgrading an
// already-imported row) — a synchronous scan over a 1000+ series NFS library
// would risk tripping a gateway timeout (e.g. the Cloudflare Tunnel's edge
// limit), so this returns immediately instead of blocking the request.
// Returns 202 {started:true} once the scan is launched, or 409
// {started:false} if a scan is already in flight (single-flight guard).
func (h *Handler) Scan(c echo.Context) error {
	started := h.svc.StartScan(c.Request().Context())
	if !started {
		return c.JSON(http.StatusConflict, scanStartedResponse{Started: false})
	}
	return c.JSON(http.StatusAccepted, scanStartedResponse{Started: true})
}

// ListImports handles GET /api/library/imports?status=&q=&limit=&offset=.
//
// The optional ?status filter must be one of pending/imported/skipped (empty
// means no filter). The optional ?q is a case-insensitive title substring
// filter (empty/absent means no filter) applied by the DB across the FULL
// staged set — so search finds a series anywhere in a 1000+ entry migration,
// not just within the loaded page — and composes with ?status. ?limit/?offset
// page the result (default 50, capped at 200 — see validatePagination).
// Returns the staged ImportEntry rows as []FoundSeriesDTO.
func (h *Handler) ListImports(c echo.Context) error {
	status, err := parseStatusFilter(c.QueryParam("status"))
	if err != nil {
		return err
	}
	search := strings.TrimSpace(c.QueryParam("q"))
	limit, offset, err := validatePagination(c.QueryParam("limit"), c.QueryParam("offset"))
	if err != nil {
		return err
	}
	out, err := h.svc.ListImports(c.Request().Context(), status, search, limit, offset)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Match handles GET /api/library/imports/match?path=.
//
// path is a REQUIRED query param — never a URL path segment, since it is a
// filesystem path (encoding it as a segment would need extra escaping this
// API deliberately avoids). An optional ?sources CSV param narrows the search
// to named source IDs (unknown IDs silently dropped — same contract as
// GET /api/search, via the shared sourcefilter.Parse). It searches the
// Suwayomi sources for the staged entry's title and returns the grouped
// candidates as []imports.SearchGroupDTO, so the owner can pick one to pass as
// `match` on the subsequent Import call.
func (h *Handler) Match(c echo.Context) error {
	path, err := validatePath(c.QueryParam("path"))
	if err != nil {
		return err
	}
	sourceIDs := sourcefilter.Parse(c.QueryParam("sources"))
	out, err := h.svc.MatchCandidates(c.Request().Context(), path, sourceIDs)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Import handles POST /api/library/import.
//
// The body carries {path, matches?}: path (required) identifies a previously
// staged ImportEntry; matches (optional) is a LIST of owner-chosen Suwayomi
// sources to attach at import time (each source non-blank + mangaId > 0;
// scanlator optional) — attached via library.AddProviders, each landing at an
// importance strictly below the disk provider's (decision E). An
// empty/absent matches list is a valid import-only request (no attach).
// Returns the imported series.SeriesDetailDTO (§16 round-trip).
func (h *Handler) Import(c echo.Context) error {
	var body importBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := validateImportBody(body); err != nil {
		return err
	}

	refs := make([]library.ProviderRef, len(body.Matches))
	for i, m := range body.Matches {
		refs[i] = library.ProviderRef{Source: m.Source, MangaID: m.MangaID, URL: m.URL, Scanlator: m.Scanlator}
	}

	out, err := h.svc.Import(c.Request().Context(), body.Path, refs)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Skip handles POST /api/library/imports/skip.
//
// The body carries {path}: path (required) identifies a previously staged
// ImportEntry (as returned by scan/list) that the owner wants to leave on
// disk without importing. Purely a status flip — no disk I/O, no row
// deletion (never-auto-delete invariant). Returns 204 on success.
func (h *Handler) Skip(c echo.Context) error {
	var body skipBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	path, err := validateSkipRequest(body)
	if err != nil {
		return err
	}

	if err := h.svc.Skip(c.Request().Context(), path); err != nil {
		return mapServiceError(err)
	}
	return c.NoContent(http.StatusNoContent)
}

// Batch handles POST /api/library/import/batch.
//
// The body carries {paths:[]string} — a bulk "import all remaining as
// disk-only" action for a 1000+ series migration, so the owner fires ONE
// request instead of N sequential POST /api/library/import calls. Each path
// is disk-only imported (mirrors Import with no match); a bad path's
// failure is recorded per-entry in the returned library.BatchResult and
// never aborts the rest of the batch (partial success — see
// library.Service.ImportBatch). Always 200 on a well-formed request: the
// per-path failures ARE the result, not a top-level error.
func (h *Handler) Batch(c echo.Context) error {
	var body batchImportBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	paths, err := validateBatch(body)
	if err != nil {
		return err
	}

	out, err := h.svc.ImportBatch(c.Request().Context(), paths)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// AddProvider handles POST /api/series/:id/providers.
//
// It attaches an additional engine-host source {source, url, importance} to
// an EXISTING series (upgrade-aware — see library.Service.AddProvider) and
// returns the refreshed series.SeriesDetailDTO. mangaId is bound but unused
// (kept for FE wire compatibility — see addProviderBody's doc comment).
func (h *Handler) AddProvider(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	var body addProviderBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := validateAddProviderBody(body); err != nil {
		return err
	}

	out, err := h.svc.AddProvider(c.Request().Context(), id, body.Source, body.URL, body.Importance, body.Scanlator)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// AddProviders handles POST /api/series/:id/providers/batch.
//
// It attaches SEVERAL Suwayomi sources to an EXISTING series in one call
// (body: {"providers":[{"source","mangaId","scanlator"}]}, ordered
// best-first) — the batch counterpart of AddProvider (see
// library.Service.AddProviders): each ref lands at an importance strictly
// below the series' existing providers, assigned by the service, not the
// caller. Returns the refreshed series.SeriesDetailDTO (§16).
func (h *Handler) AddProviders(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	var body addProvidersBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := validateProviderRefs(body.Providers); err != nil {
		return err
	}

	refs := make([]library.ProviderRef, len(body.Providers))
	for i, p := range body.Providers {
		refs[i] = library.ProviderRef{Source: p.Source, MangaID: p.MangaID, URL: p.URL, Scanlator: p.Scanlator}
	}

	out, err := h.svc.AddProviders(c.Request().Context(), id, refs)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// MatchDiskProvider handles POST /api/series/:id/providers/:providerId/match.
//
// It attributes a series' EXISTING on-disk chapters — currently satisfied by
// the unlinked disk-origin provider at :providerId (see ProviderDTO.Linked)
// — to a real engine-host source {source, url, scanlator, importance}
// WITHOUT re-downloading them (see library.Service.MatchDiskProvider). Returns
// the refreshed series.SeriesDetailDTO (§16). mangaId is bound but unused
// (kept for FE wire compatibility).
func (h *Handler) MatchDiskProvider(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	providerID, err := validateID(c.Param("providerId"))
	if err != nil {
		return err
	}
	var body addProviderBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	if err := validateAddProviderBody(body); err != nil {
		return err
	}

	out, err := h.svc.MatchDiskProvider(c.Request().Context(), id, providerID, body.Source, body.URL, body.Scanlator, body.Importance)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// providerDedupResponse is the wire shape returned by POST
// /api/series/:id/providers/dedup: merged is how many drifted disk/live source
// pairs were folded into one, skipped is how many were left alone (a matching
// linked twin with an empty feed — merging would orphan the disk chapters), and
// series is the refreshed detail after the cleanup.
type providerDedupResponse struct {
	Merged  int                    `json:"merged"`
	Skipped int                    `json:"skipped"`
	Series  series.SeriesDetailDTO `json:"series"`
}

// DedupProviders handles POST /api/series/:id/providers/dedup.
//
// It folds every pair of same-physical-source providers on the series — an
// unlinked disk-origin provider and its already-drifted linked twin (same
// display name + scanlator) — into one row WITHOUT re-downloading (see
// library.Service.DedupProviders), then returns the merged/skipped counts plus
// the refreshed series detail (§16 round-trip).
func (h *Handler) DedupProviders(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}

	merged, skipped, err := h.svc.DedupProviders(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err)
	}

	detail, err := h.svc.SeriesDetail(c.Request().Context(), id)
	if err != nil {
		return mapServiceError(err)
	}

	return c.JSON(http.StatusOK, providerDedupResponse{Merged: merged, Skipped: skipped, Series: detail})
}

// dedupAllTimeout bounds the detached background library-wide dedup sweep a
// DedupAllProviders request kicks off. Dedup is DB + CBZ-relabel only (no live
// source fetch), so it is far faster than a warm-up pass, but the cap guarantees
// the background goroutine + its context can never leak.
const dedupAllTimeout = 10 * time.Minute

// libraryDedupStartedResponse is the JSON body of POST /api/library/dedup-providers:
// {"started":true} on 202 once the async library-wide dedup sweep is launched.
// The sweep runs detached (it can touch every series); per-series outcomes are
// logged server-side and surface in each series' refreshed detail on next view.
type libraryDedupStartedResponse struct {
	Started bool `json:"started"`
}

// DedupAllProviders handles POST /api/library/dedup-providers. The sweep runs
// DedupProviders across every series; over a large library that can take a
// while, so — like POST /api/sources/warmup — it runs on a detached,
// time-bounded background goroutine and returns 202 immediately. A background
// failure is logged, never returned.
func (h *Handler) DedupAllProviders(c echo.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request().Context()), dedupAllTimeout)
	go func() {
		defer cancel()
		processed, merged, skipped, err := h.svc.DedupAllProviders(ctx)
		if err != nil {
			slog.WarnContext(ctx, "library: background dedup sweep failed", "err", err)
			return
		}
		slog.InfoContext(ctx, "library: dedup sweep complete", "series_processed", processed, "merged", merged, "skipped", skipped)
	}()
	return c.JSON(http.StatusAccepted, libraryDedupStartedResponse{Started: true})
}

// GetPrefs handles GET /api/library/prefs.
//
// Returns the owner's persisted library-list view state (sort field +
// direction + active toggle-filters), or the defaults when none are stored.
// A single-owner server-side preference so the view survives a refresh/restart
// and is shared cross-device.
func (h *Handler) GetPrefs(c echo.Context) error {
	out, err := h.svc.GetPrefs(c.Request().Context())
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// PutPrefs handles PUT /api/library/prefs.
//
// Replaces the owner's library-list view state. The body is a full
// library.LibraryPrefs {sortKey, sortDir, filters}; an unknown sortKey or a
// bad direction is rejected 400 (ErrInvalidPrefs, fail-closed — nothing is
// written). Returns the stored value (§16 round-trip). The frontend saves
// best-effort on change, so a failure here never breaks the grid.
func (h *Handler) PutPrefs(c echo.Context) error {
	var body library.LibraryPrefs
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	out, err := h.svc.SetPrefs(c.Request().Context(), body)
	if err != nil {
		return mapServiceError(err)
	}
	return c.JSON(http.StatusOK, out)
}

// mapServiceError translates a library.Service sentinel error into the
// matching HTTP status, leaving any unexpected error to fall through to the
// central middleware as a 500. ErrSeriesNotFound / ErrEntryNotFound → 404;
// ErrProviderAlreadyPresent → 409; ErrSourceNotFound → 404 (a TRUE membership
// miss only — an unparseable id or one absent from the engine host's live
// Sources(); AddProvider wraps the parse-miss via errors.Join so errors.Is still
// matches through the join); ErrSourceUnavailable → 503 (the source exists but
// its anti-ban circuit-breaker is cooled down — retry shortly); ErrSourceUpstream
// → 502 via httperr.Upstream (a genuine engine-host fetch failure — the real
// reason surfaces instead of the old phantom 404); ErrProviderNotInSeries / ErrNotADiskProvider
// (MatchDiskProvider) → 400 (a malformed request referencing the wrong
// provider or one that is already a real, linked source); ErrNoProviders
// (library.AddProviders called with an empty batch) → 400 — reachable from
// Import only in principle (validateImportBody already rejects an empty-body
// match entry per-item, but an all-empty non-nil matches slice with zero
// elements is caught by the handler building a zero-length refs slice, which
// AddProviders itself guards).
func mapServiceError(err error) error {
	if mapped, ok := mapSourceError(err); ok {
		return mapped
	}
	switch {
	case errors.Is(err, library.ErrSeriesNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "series not found")
	case errors.Is(err, library.ErrEntryNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "import entry not found")
	case errors.Is(err, library.ErrProviderAlreadyPresent):
		return echo.NewHTTPError(http.StatusConflict, "provider already attached to series")
	case errors.Is(err, library.ErrProviderNotInSeries):
		return echo.NewHTTPError(http.StatusBadRequest, "provider does not belong to series")
	case errors.Is(err, library.ErrNotADiskProvider):
		return echo.NewHTTPError(http.StatusBadRequest, "provider is not an unlinked disk-origin provider")
	case errors.Is(err, library.ErrNoProviders):
		return echo.NewHTTPError(http.StatusBadRequest, "no providers supplied")
	case errors.Is(err, library.ErrInvalidPrefs):
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	default:
		return err
	}
}

// mapSourceError maps the source-attach error family (AddProvider /
// MatchDiskProvider) to its HTTP status, returning ok=false when err is not one
// of them so mapServiceError can fall through to the rest. Split out of
// mapServiceError to keep both within the fleet cyclop budget (§2 side benefit:
// the honest source taxonomy lives in one place). ErrSourceNotFound → 404 (a TRUE
// membership miss only); ErrSourceUnavailable → 503 (cooled-down breaker, retry
// shortly — the sentinel's own text is caller-safe and names the source);
// ErrSourceUpstream → 502 via httperr.Upstream (a genuine engine-host fetch
// failure — the real reason surfaces instead of the old phantom 404).
func mapSourceError(err error) (error, bool) {
	switch {
	case errors.Is(err, library.ErrSourceNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "source not found"), true
	case errors.Is(err, library.ErrSourceUnavailable):
		return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error()), true
	case errors.Is(err, library.ErrSourceUpstream):
		return httperr.Upstream(err), true
	}
	return nil, false
}
