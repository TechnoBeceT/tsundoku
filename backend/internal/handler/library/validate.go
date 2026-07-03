package library

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// importBody is the wire shape for POST /api/library/import.
type importBody struct {
	Path  string     `json:"path"`
	Match *matchBody `json:"match,omitempty"`
}

// matchBody is the optional owner-chosen Suwayomi source to attach at import
// time (POST /api/library/import's "match" field).
type matchBody struct {
	Source     string `json:"source"`
	MangaID    int    `json:"mangaId"`
	Importance int    `json:"importance"`
}

// addProviderBody is the wire shape for POST /api/series/:id/providers.
type addProviderBody struct {
	Source     string `json:"source"`
	MangaID    int    `json:"mangaId"`
	Importance int    `json:"importance"`
}

// skipBody is the wire shape for POST /api/library/imports/skip.
type skipBody struct {
	Path string `json:"path"`
}

// validateID parses a required UUID path param (the target series id).
func validateID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid series id")
	}
	return id, nil
}

// validatePath validates a REQUIRED, non-empty path query param. path is a
// filesystem path (from a prior Scan), so it is carried as a query/body
// param rather than a URL path segment — never URL-encoded as a segment.
func validatePath(raw string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "path is required")
	}
	return path, nil
}

// validateImportBody validates the POST /api/library/import body: path is
// required (non-blank); match, if present, requires a non-empty source, a
// positive mangaId, and an importance >= 1 (delegates to validateMatch).
func validateImportBody(body importBody) error {
	if strings.TrimSpace(body.Path) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "path is required")
	}
	if body.Match != nil {
		return validateMatch(*body.Match)
	}
	return nil
}

// validateAddProviderBody validates the POST /api/series/:id/providers body:
// a non-empty source, a positive mangaId, and an importance >= 1.
func validateAddProviderBody(body addProviderBody) error {
	return validateMatch(matchBody(body))
}

// validateMatch is the shared source/mangaId/importance validation reused by
// both validateImportBody's optional match and validateAddProviderBody (§2
// DRY — addProviderBody and matchBody share the identical field shape, so a
// plain type conversion feeds both callers through the one check).
func validateMatch(m matchBody) error {
	if strings.TrimSpace(m.Source) == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "source is required")
	}
	if m.MangaID <= 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "mangaId must be > 0")
	}
	if m.Importance < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, "importance must be >= 1")
	}
	return nil
}

// validateSkipRequest validates the POST /api/library/imports/skip body:
// path is required (non-blank) — delegates to the same validatePath check
// used for the match handler's ?path query param (§2 DRY).
func validateSkipRequest(body skipBody) (string, error) {
	return validatePath(body.Path)
}

// parseStatusFilter parses the optional ?status filter. An empty value is
// allowed (no filter); otherwise it must be one of pending/imported/skipped
// (the ImportEntry.status enum) — an unknown value yields a 400 naming it.
func parseStatusFilter(raw string) (string, error) {
	switch raw {
	case "", "pending", "imported", "skipped":
		return raw, nil
	default:
		return "", echo.NewHTTPError(http.StatusBadRequest, "unknown status: "+raw)
	}
}
