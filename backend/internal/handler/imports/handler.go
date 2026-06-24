package imports

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/imports"
	seriessvc "github.com/technobecet/tsundoku/internal/series"
)

// Handler holds the dependencies for the imports HTTP handlers.
// All business logic lives in imports.Service and series.Service; this handler
// is thin — it binds, validates, calls the service, and renders the DTO.
type Handler struct {
	svc    *imports.Service
	series *seriessvc.Service
}

// NewHandler constructs a Handler bound to an imports.Service and a
// series.Service (needed to render SeriesDetailDTO after Adopt).
func NewHandler(svc *imports.Service, series *seriessvc.Service) *Handler {
	return &Handler{svc: svc, series: series}
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
// service (documented choice: see validate.go parseSourcesFilter).
// Returns []SearchGroupDTO grouped by title similarity.
func (h *Handler) Search(c echo.Context) error {
	q, err := parseQuery(c.QueryParam("q"))
	if err != nil {
		return err
	}
	sourceIDs := parseSourcesFilter(c.QueryParam("sources"))

	out, err := h.svc.Search(c.Request().Context(), q, sourceIDs)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, out)
}

// InspectChapters handles GET /api/sources/:sourceId/manga/:mangaId/chapters.
//
// It parses :mangaId as an integer (non-integer → 400) and returns the live
// chapter list as []ChapterInspectDTO. :sourceId is passed to the service for
// routing context (currently unused by the service implementation).
func (h *Handler) InspectChapters(c echo.Context) error {
	mangaID, err := parseMangaID(c.Param("mangaId"))
	if err != nil {
		return err
	}
	sourceID := c.Param("sourceId")

	out, err := h.svc.InspectChapters(c.Request().Context(), sourceID, mangaID)
	if err != nil {
		return err
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
			Importance: p.Importance,
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
	return c.JSON(http.StatusCreated, detail)
}
