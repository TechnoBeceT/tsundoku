package imports

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/handler/sourcefilter"
	"github.com/technobecet/tsundoku/internal/imports"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourcecover"
)

// SeriesSyncer runs the per-series instant refresh+detect layer (GAP-113) for a
// newly adopted series. *seriessync.Orchestrator satisfies it. Kept as a local
// interface so this handler never imports seriessync, and nil is a valid value (the
// scoped layer is simply skipped — the whole-library sweep still covers the series).
type SeriesSyncer interface {
	SyncSeries(ctx context.Context, seriesID uuid.UUID)
}

// Handler holds the dependencies for the imports HTTP handlers.
// All business logic lives in imports.Service and series.Service; this handler
// is thin — it binds, validates, calls the service, and renders the DTO.
type Handler struct {
	svc        *imports.Service
	series     *seriessvc.Service
	trigger    func()
	coverCache *sourcecover.Cache
	seriesSync SeriesSyncer
}

// NewHandler constructs a Handler bound to an imports.Service, a series.Service
// (to render SeriesDetailDTO after Adopt), an auto-converge trigger (called
// after a successful adopt to kick an immediate download/upgrade cycle — M5),
// and a sourcecover.Cache (used by SourceCover to proxy a source-manga cover
// image through the engine host, disk-cached and fail-fast bounded — same
// role as series.Handler's `coverCache`, see ProviderCover and GAP-085).
func NewHandler(svc *imports.Service, series *seriessvc.Service, trigger func(), coverCache *sourcecover.Cache) *Handler {
	return &Handler{svc: svc, series: series, trigger: trigger, coverCache: coverCache}
}

// WithSeriesSync attaches the per-series instant refresh+detect orchestrator
// (GAP-113), fired after a successful Adopt so the new series' feeds are re-synced
// and its upgradable chapters flagged immediately. Returns the receiver for
// chaining off NewHandler. A nil syncer (the base constructor) skips the scoped
// layer — existing behaviour is unchanged.
func (h *Handler) WithSeriesSync(s SeriesSyncer) *Handler {
	h.seriesSync = s
	return h
}

// Sources handles GET /api/sources.
//
// It returns all Suwayomi sources as []SourceDTO. No query params.
func (h *Handler) Sources(c echo.Context) error {
	out, err := h.svc.Sources(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// SourceCover handles GET /api/sources/:sourceId/cover?url=<thumbnailUrl>.
//
// Discover/Search cards render a source-manga cover straight from the DTO's
// thumbnailUrl. An open-CDN source (e.g. Asura) tolerates the browser fetching
// that URL directly, but a Cloudflare/hotlink-protected source (e.g. The
// Blank) 403s a raw browser request — the card renders blank. This mirrors
// series.Handler.ProviderCover (the SAME coverproxy.StreamEngineCached
// primitive): it re-fetches the image through the engine host, whose outbound
// HTTP client carries the source's cf_clearance, then streams the bytes back
// same-origin so the SPA's cookie session covers auth with a plain <img src>
// — no header needed. A malformed :sourceId or a blank ?url= is a 400; an
// engine fetch failure is a 502; a cold fetch that cannot resolve within the
// fail-fast deadline is a 504 (never a false 200, never a held connection).
//
// GAP-085: a Discover/library grid renders ~15 covers at once. Before this
// cache existed EVERY render re-fetched EVERY cover LIVE from the engine host
// (SourceCalls.image() on the engine host is cacheless), and that burst was
// enough to trip Cloudflare's per-source rate-limiting on a protected source
// — each slow re-solve held a same-origin connection open, saturating the
// browser's per-host connection cap and hanging the whole SPA. Routing
// through coverCache (internal/sourcecover) fetches each (sourceID, url) at
// most once ever (see the package doc for why no TTL is needed) and bounds
// any genuine cold miss with a fail-fast deadline + bounded concurrency, so a
// burst can never reproduce that hang.
func (h *Handler) SourceCover(c echo.Context) error {
	sourceID, err := parseSourceID(c.Param("sourceId"))
	if err != nil {
		return err
	}
	// GAP-146: never fetch a cover for an owner-PAUSED source (defense-in-depth —
	// its cards are already gone from every picker). A light disabled-set check
	// that deliberately avoids resolving the full source list, so the hot cached
	// cover path keeps its zero-engine-call property (see EnsureSourceEnabled).
	if err := h.svc.EnsureSourceEnabled(c.Request().Context(), sourceID); err != nil {
		if errors.Is(err, imports.ErrSourceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "source not found")
		}
		return err
	}
	url, err := parseCoverURL(c.QueryParam("url"))
	if err != nil {
		return err
	}
	return coverproxy.StreamEngineCached(c, h.coverCache, sourceID, url)
}

// Search handles GET /api/search.
//
// It requires a non-empty ?q parameter. An optional ?sources CSV param narrows
// the search to named source IDs; unknown IDs are silently dropped by the
// service (documented choice: see sourcefilter.Parse).
// Returns []SearchGroupDTO grouped by title similarity.
func (h *Handler) Search(c echo.Context) error {
	q, err := parseQuery(c.QueryParam("q"))
	if err != nil {
		return err
	}
	sourceIDs := sourcefilter.Parse(c.QueryParam("sources"))

	out, err := h.svc.Search(c.Request().Context(), q, sourceIDs)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// InspectChapters handles GET /api/sources/:sourceId/manga/:mangaId/chapters?url=&title=.
//
// P2 Suwayomi-removal (slice 3b): the backend is URL-addressed — it requires a
// REQUIRED ?url query param (the exact source-owned serialized manga address)
// and returns the live chapter list as []ChapterInspectDTO. :mangaId stays in the route (FE
// compat) but is bound/ignored; a request that only sends :mangaId (the
// not-yet-updated frontend) gets a clean 400 until slice 3b-FE sends ?url=.
//
// ?title= is OPTIONAL free text (the manga's display title, e.g. from a
// Discover candidate the caller already has in hand) — passing it improves
// the engine host's chapter-number recognition AND lets this preview populate
// the SAME shared chapter-cache entry a later Adopt for the same manga will
// hit (see imports.Service.fetchChapters's doc comment). Omitting it is safe
// (recognition still runs, just without the title-strip step); no validation
// beyond trimming, since it feeds a display heuristic, not an identity key.
func (h *Handler) InspectChapters(c echo.Context) error {
	sourceID := c.Param("sourceId")
	url, err := parseChapterURL(c.QueryParam("url"))
	if err != nil {
		return err
	}
	title := parseOptionalTitle(c.QueryParam("title"))
	mode, webURL, err := parseAddressContext(c.QueryParam("addressMode"), c.QueryParam("webUrl"))
	if err != nil {
		return err
	}

	out, err := h.svc.InspectChaptersRef(c.Request().Context(), sourceID, url, mode, webURL, title)
	if err != nil {
		// An unknown OR owner-disabled source is a 404 (GAP-146), mirroring
		// Browse/Details/Breakdown; any other failure is a genuine upstream/source
		// problem and surfaces through the central error middleware.
		if errors.Is(err, imports.ErrSourceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "source not found")
		}
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// Browse handles GET /api/sources/:sourceId/browse?type=popular|latest&page=N.
//
// It resolves :sourceId from the path, validates the required ?type enum and the
// optional ?page (default 1, must be >= 1), then returns one page of the source's
// catalog listing as a BrowseResultDTO. An unknown source maps to 404; any other
// service/upstream error surfaces through the central error middleware (500).
func (h *Handler) Browse(c echo.Context) error {
	sourceID := c.Param("sourceId")
	browseType, err := parseBrowseType(c.QueryParam("type"))
	if err != nil {
		return err
	}
	page, err := parseBrowsePage(c.QueryParam("page"))
	if err != nil {
		return err
	}

	out, err := h.svc.Browse(c.Request().Context(), sourceID, browseType, page)
	if err != nil {
		if errors.Is(err, imports.ErrSourceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "source not found")
		}
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// Details handles GET /api/sources/:sourceId/manga/:mangaId/details?url=.
//
// It FORCES a live details fetch from the upstream source (via
// imports.Service.MangaDetails → sourceengine.Client.MangaDetails) and
// returns the enriched candidate as a SearchCandidateDTO — the same shape
// Search/Browse return, so the frontend Discover hover preview can merge it
// straight into an already-rendered candidate. Call this ON DEMAND for one
// hovered manga at a time; never for every row of a search/browse page.
//
// P2 Suwayomi-removal (slice 3b): requires a REQUIRED ?url query param (see
// InspectChapters's doc comment for the same :mangaId-kept-but-ignored /
// ?url=-required transition). An unknown :sourceId maps to 404 (mirrors
// Browse); any other failure is a genuine upstream source problem and maps to
// 502 (mirrors the cover-proxy error mapping in cover.go), so a source outage
// never surfaces as a false 200.
func (h *Handler) Details(c echo.Context) error {
	sourceID := c.Param("sourceId")
	url, err := parseChapterURL(c.QueryParam("url"))
	if err != nil {
		return err
	}
	mode, webURL, err := parseAddressContext(c.QueryParam("addressMode"), c.QueryParam("webUrl"))
	if err != nil {
		return err
	}

	out, err := h.svc.MangaDetailsRef(c.Request().Context(), sourceID, url, mode, webURL)
	if err != nil {
		if errors.Is(err, imports.ErrSourceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "source not found")
		}
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, out)
}

// breakdownResponse is the wire shape for GET .../breakdown (GAP-140). It
// embeds the persisted per-scanlator payload (Total/Scanlators, unchanged
// from before this endpoint went async) and adds the snapshot's own state, so
// a client can tell "ready and fresh" from "still converging" from "broken"
// without a separate poll:
//   - status: "pending" | "ready" | "failed"
//   - computedAt: RFC3339 as-of instant of the payload; "" while pending or
//     never computed — the owner's only signal for how stale a snapshot is.
//   - error: the failure reason when status is "failed"; omitted otherwise.
type breakdownResponse struct {
	imports.SourceBreakdownDTO
	Status     string `json:"status"`
	ComputedAt string `json:"computedAt,omitempty"`
	Error      string `json:"error,omitempty"`
}

// newBreakdownResponse renders a CoverageSnapshot as the wire shape above.
//
// The ONE normalization point for imports.SourceBreakdownDTO's own invariant
// ("Scanlators is always non-nil (JSON []), never null" — dto.go) once this
// handler started embedding a CoverageSnapshot instead of a freshly-computed
// DTO: the pending fast-path timeout and a failed snapshot both persist/return
// a zero-value Payload (Coverage's timeout branch, loadCoverage's empty-payload
// branch), whose Scanlators is a nil Go slice. Left unguarded that marshals to
// JSON `null` (no omitempty on the field), calcifying a defect into the public
// contract — every caller (a script, a mobile client, a future composable)
// would need its own null-check before it dared call .map() on the field.
// Every response renders through this one function, so the fix belongs here,
// not scattered across the two zero-value construction sites.
func newBreakdownResponse(snap imports.CoverageSnapshot) breakdownResponse {
	resp := breakdownResponse{SourceBreakdownDTO: snap.Payload, Status: snap.Status, Error: snap.LastError}
	if resp.Scanlators == nil {
		resp.Scanlators = []imports.ScanlatorCoverageDTO{}
	}
	if snap.ComputedAt != nil {
		resp.ComputedAt = snap.ComputedAt.UTC().Format(time.RFC3339)
	}
	return resp
}

// Breakdown handles GET /api/sources/:sourceId/manga/:mangaId/breakdown?url=&title=.
//
// GAP-140: this used to fetch the live chapter feed for (sourceId, url) and
// group it by scanlator synchronously. Since a source moved behind JS
// Detection, that walk costs one WebView navigation PER PAGE — ~330 for a
// 1,301-chapter series (~15-20 minutes) — which no HTTP timeout tolerates and
// which cached nothing on failure, so every retry paid full price. The
// handler now reads a PERSISTED snapshot via imports.Service.Coverage: a
// READY one is returned immediately; otherwise the walk is started in the
// background and this request waits a short bounded window, falling through
// to a `pending` body if the walk is still running. Small series therefore
// still feel synchronous; only expensive ones go async, with
// imports.coverage.done delivering the eventual outcome over SSE.
//
// An unknown :sourceId still maps to 404 (mirrors Details/Browse):
// imports.Service.Coverage resolves the source synchronously, before
// touching the coverage store, because that resolve is a fast in-memory
// check — a genuinely unknown source is a client error, not something worth
// a bounded wait and a persisted `failed` snapshot. Once the source is known
// to exist, any OTHER walk failure (upstream error, …) is no longer mapped
// onto an HTTP status — it is persisted and rendered as an ordinary snapshot
// with status "failed" and a human-readable error, because the same code
// path that returns `pending` for a slow walk cannot also tell a caller
// "this failed" via an error return without conflating the two. ?url= is a
// REQUIRED query param (see InspectChapters's doc comment for the
// transition); ?title= is OPTIONAL — same free-text/cache-sharing contract
// as InspectChapters's.
//
// ?refresh (OPTIONAL boolean, GAP-140 follow-up) forces a recomputation past
// the `ready`/`failed`-cooldown admission guards — see parseRefresh and
// imports.Coverage's `refresh` parameter for exactly what it bypasses. It
// NEVER duplicates a walk already in flight: a refresh arriving while a
// `pending` row is live is served that same `pending` body, same as a plain
// GET. An unparseable value is a 400, not a silent default.
func (h *Handler) Breakdown(c echo.Context) error {
	sourceID := c.Param("sourceId")
	url, err := parseChapterURL(c.QueryParam("url"))
	if err != nil {
		return err
	}
	title := parseOptionalTitle(c.QueryParam("title"))
	mode, webURL, err := parseAddressContext(c.QueryParam("addressMode"), c.QueryParam("webUrl"))
	if err != nil {
		return err
	}
	refresh, err := parseRefresh(c.QueryParam("refresh"))
	if err != nil {
		return err
	}

	snap, err := h.svc.CoverageRef(c.Request().Context(), sourceID, url, mode, webURL, title, refresh)
	if err != nil {
		if errors.Is(err, imports.ErrSourceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "source not found")
		}
		return err
	}
	return c.JSON(http.StatusOK, newBreakdownResponse(snap))
}

// Adopt handles POST /api/series.
//
// It binds and validates the AdoptRequest body (non-blank title, >= 1 provider,
// distinct (source, mangaId) pairs, importance >= 0, optional valid category),
// then calls imports.Service.Adopt to ingest the series. On success it loads the
// SeriesDetailDTO via series.Service.GetSeries and returns 201 so the caller
// sees the full persisted state without a refetch (§16 full round-trip).
//
// Category validation is handled entirely by validateAdoptBody (via
// entseries.CategoryValidator) before the service is ever called, so the service
// never receives an invalid category from this handler. Any error from Adopt is a
// genuine upstream/ingest/DB failure and surfaces through the central error
// middleware unchanged.
func (h *Handler) Adopt(c echo.Context) error {
	var body adoptRequestBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	// Treat a completely empty title (from a missing or null JSON field) as blank.
	body.Title = strings.TrimSpace(body.Title)
	if err := validateAdoptBody(body); err != nil {
		return err
	}

	// Map the wire body to the service request type.
	providers := make([]imports.AdoptProvider, len(body.Providers))
	for i, p := range body.Providers {
		providers[i] = imports.AdoptProvider{
			Source:      p.Source,
			MangaID:     p.MangaID,
			URL:         p.URL,
			AddressMode: p.AddressMode,
			WebURL:      p.WebURL,
			Importance:  p.Importance,
			Scanlator:   p.Scanlator,
		}
	}

	ctx := c.Request().Context()
	id, err := h.svc.Adopt(ctx, imports.AdoptRequest{
		Title:     body.Title,
		Category:  body.Category,
		Providers: providers,
	})
	if err != nil {
		return err
	}

	detail, err := h.series.GetSeries(ctx, id)
	if err != nil {
		return err
	}
	// Auto-converge: kick an immediate download/upgrade cycle so the adopted
	// series' backlog downloads now instead of at the next tick (M5).
	h.trigger()
	// Instant per-series convergence (GAP-113): re-fetch this new series' feeds and
	// flag its upgradable chapters right away rather than waiting for the 2h sweep.
	// Async + single-flight; nil when not wired (scoped layer skipped).
	if h.seriesSync != nil {
		h.seriesSync.SyncSeries(ctx, id)
	}
	return c.JSON(http.StatusCreated, detail)
}
