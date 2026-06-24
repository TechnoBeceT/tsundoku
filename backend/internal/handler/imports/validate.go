// Package imports contains the thin HTTP handlers for the imports API: listing
// sources, searching across sources, inspecting a manga's chapters, and adopting
// a manga (or a group of manga from multiple sources) into the library.
// Business logic lives in internal/imports (the service); these handlers only
// bind/parse the request, validate it, call the service, and render the DTO.
package imports

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
)

// adoptProviderRequest is the per-provider element within AdoptRequestBody. It
// mirrors imports.AdoptProvider with JSON tags for camelCase wire format.
type adoptProviderRequest struct {
	// Source is the Suwayomi source ID.
	Source string `json:"source"`
	// MangaID is the Suwayomi manga identifier within Source.
	MangaID int `json:"mangaId"`
	// Importance is the provider rank (higher = preferred); must be >= 0.
	Importance int `json:"importance"`
}

// adoptRequestBody is the JSON body for POST /api/series.
type adoptRequestBody struct {
	// Title is the canonical series title; must be non-blank.
	Title string `json:"title"`
	// Category is the optional target category (one of the enum values).
	// Omit or send "" to use the schema default (Other).
	Category string `json:"category"`
	// Providers is the ordered list of (source, manga) pairs; must have >= 1 entry.
	Providers []adoptProviderRequest `json:"providers"`
}

// parseQuery validates and returns the ?q search query parameter. An empty or
// absent value yields a 400 echo.HTTPError.
func parseQuery(raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "q is required and must be non-empty")
	}
	return q, nil
}

// parseSourcesFilter parses the optional ?sources CSV query parameter. An empty
// or absent parameter returns nil (meaning "all sources"). Non-empty tokens that
// appear after splitting are returned as-is; unknown source IDs are silently
// dropped by the service's resolveSources (documented choice: validating against
// the live client list here would require a round-trip before the search itself).
func parseSourcesFilter(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseMangaID parses the :mangaId path parameter as a positive integer. A
// non-integer value yields a 400 echo.HTTPError.
func parseMangaID(raw string) (int, error) {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "mangaId must be an integer")
	}
	return v, nil
}

// validateAdoptBody validates the parsed AdoptRequestBody:
//   - title must be non-blank.
//   - providers must have >= 1 entry.
//   - each provider's importance must be >= 0.
//   - each (source, mangaId) pair must be distinct (no duplicates).
//   - category, if non-empty, must be a legal enum value.
func validateAdoptBody(req adoptRequestBody) error {
	if strings.TrimSpace(req.Title) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "title is required and must be non-blank")
	}
	if len(req.Providers) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "providers must have at least one entry")
	}
	seen := make(map[string]bool, len(req.Providers))
	for _, p := range req.Providers {
		if p.Importance < 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "provider importance must be >= 0")
		}
		key := p.Source + ":" + strconv.Itoa(p.MangaID)
		if seen[key] {
			return echo.NewHTTPError(http.StatusBadRequest, "duplicate (source, mangaId) pair in providers")
		}
		seen[key] = true
	}
	if req.Category != "" {
		if err := entseries.CategoryValidator(entseries.Category(req.Category)); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid category: "+req.Category)
		}
	}
	return nil
}
