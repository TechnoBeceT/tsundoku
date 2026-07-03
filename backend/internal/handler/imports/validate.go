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
	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/suwayomi"
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

// parseBrowseType maps the ?type query parameter to a suwayomi.BrowseType.
// "popular" → POPULAR, "latest" → LATEST; any other value (including empty)
// yields a 400 echo.HTTPError — type is a required closed enum.
func parseBrowseType(raw string) (suwayomi.BrowseType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "popular":
		return suwayomi.BrowsePopular, nil
	case "latest":
		return suwayomi.BrowseLatest, nil
	default:
		return "", echo.NewHTTPError(http.StatusBadRequest, "type is required and must be one of: popular, latest")
	}
}

// parseBrowsePage parses the optional ?page query parameter. An empty value
// defaults to 1; a non-integer or a value < 1 yields a 400 echo.HTTPError
// (mirrors the pagination-default discipline used elsewhere). page is 1-based.
func parseBrowsePage(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 1, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "page must be an integer >= 1")
	}
	return v, nil
}

// parseMangaID parses the :mangaId path parameter as a positive integer. A
// non-integer value, or an integer < 1, yields a 400 echo.HTTPError — without
// this guard a value like 0 or -1 parsed cleanly but surfaced downstream as a
// raw Suwayomi 502 instead of a clean validation error.
func parseMangaID(raw string) (int, error) {
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "mangaId must be an integer")
	}
	if v < 1 {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "mangaId must be a positive integer")
	}
	return v, nil
}

// validateAdoptBody validates the parsed AdoptRequestBody:
//   - title must be non-blank.
//   - providers must have >= 1 entry.
//   - each provider's importance must be >= 0.
//   - each source must be distinct across providers (a series may carry at most
//     one provider per source; duplicate sources — even with different mangaIds —
//     would silently collapse onto a single SeriesProvider row).
//   - category, if non-empty, must be a legal enum value.
func validateAdoptBody(req adoptRequestBody) error {
	if strings.TrimSpace(req.Title) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "title is required and must be non-blank")
	}
	if len(req.Providers) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "providers must have at least one entry")
	}
	seenSource := make(map[string]bool, len(req.Providers))
	for _, p := range req.Providers {
		if p.Importance < 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "provider importance must be >= 0")
		}
		if seenSource[p.Source] {
			return echo.NewHTTPError(http.StatusBadRequest, "duplicate source in providers: each source may appear at most once")
		}
		seenSource[p.Source] = true
	}
	if req.Category != "" {
		if _, err := category.ValidateName(req.Category); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid category: "+req.Category)
		}
	}
	return nil
}
