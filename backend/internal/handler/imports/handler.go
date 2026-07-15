package imports

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/handler/sourcefilter"
	"github.com/technobecet/tsundoku/internal/imports"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// Handler holds the dependencies for the imports HTTP handlers.
// All business logic lives in imports.Service and series.Service; this handler
// is thin — it binds, validates, calls the service, and renders the DTO. sw is
// held directly (cover-proxy style, like handler/series and handler/suwayomi)
// so MangaCover can stream a source-manga thumbnail without a Tsundoku service
// round-trip.
type Handler struct {
	svc     *imports.Service
	series  *seriessvc.Service
	trigger func()
	sw      suwayomi.Client
}

// NewHandler constructs a Handler bound to an imports.Service, a series.Service
// (to render SeriesDetailDTO after Adopt), an auto-converge trigger (called
// after a successful adopt to kick an immediate download/upgrade cycle — M5),
// and a suwayomi.Client (used by MangaCover to proxy a source-manga thumbnail).
func NewHandler(svc *imports.Service, series *seriessvc.Service, trigger func(), sw suwayomi.Client) *Handler {
	return &Handler{svc: svc, series: series, trigger: trigger, sw: sw}
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

// InspectChapters handles GET /api/sources/:sourceId/manga/:mangaId/chapters?url=.
//
// P2 Suwayomi-removal (slice 3b): the backend is URL-addressed — it requires a
// REQUIRED ?url query param (the source-relative manga URL) and returns the
// live chapter list as []ChapterInspectDTO. :mangaId stays in the route (FE
// compat) but is bound/ignored; a request that only sends :mangaId (the
// not-yet-updated frontend) gets a clean 400 until slice 3b-FE sends ?url=.
func (h *Handler) InspectChapters(c echo.Context) error {
	sourceID := c.Param("sourceId")
	url, err := parseChapterURL(c.QueryParam("url"))
	if err != nil {
		return err
	}

	out, err := h.svc.InspectChapters(c.Request().Context(), sourceID, url)
	if err != nil {
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

	out, err := h.svc.MangaDetails(c.Request().Context(), sourceID, url)
	if err != nil {
		if errors.Is(err, imports.ErrSourceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "source not found")
		}
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, out)
}

// Breakdown handles GET /api/sources/:sourceId/manga/:mangaId/breakdown?url=.
//
// It fetches the live chapter feed for (sourceId, url) and groups it by
// scanlator, returning a SourceBreakdownDTO so the adopt UI can auto-split a
// source into per-scanlator rows with counts + display ranges.
//
// P2 Suwayomi-removal (slice 3b): requires a REQUIRED ?url query param (see
// InspectChapters's doc comment for the transition). An unknown :sourceId maps
// to 404 (mirrors Details); any other failure is a genuine upstream source
// problem and maps to 502 via the shared httperr.Upstream (mirrors Details),
// so a source outage never surfaces as a false 200.
func (h *Handler) Breakdown(c echo.Context) error {
	sourceID := c.Param("sourceId")
	url, err := parseChapterURL(c.QueryParam("url"))
	if err != nil {
		return err
	}

	out, err := h.svc.SourceBreakdown(c.Request().Context(), sourceID, url)
	if err != nil {
		if errors.Is(err, imports.ErrSourceNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "source not found")
		}
		return httperr.Upstream(err)
	}
	return c.JSON(http.StatusOK, out)
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
			Source:     p.Source,
			MangaID:    p.MangaID,
			URL:        p.URL,
			Importance: p.Importance,
			Scanlator:  p.Scanlator,
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
	return c.JSON(http.StatusCreated, detail)
}
