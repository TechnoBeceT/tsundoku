package extensions

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// ReposUpdateRequest is the PUT /api/suwayomi/extensions/repos body. Repos is a
// plain slice so that BOTH a missing key and an explicit null unmarshal to nil
// (rejected as "must be a JSON array"), while an explicit empty array [] stays
// non-nil and is accepted (clear-all, replace semantics).
type ReposUpdateRequest struct {
	// Repos is the full replacement repo URL list (PUT = replace, not merge).
	Repos []string `json:"repos"`
}

// validatePkgName trims and requires a non-empty pkgName path param. A blank
// value is a 400; the trimmed value is returned for use as the extension identity.
func validatePkgName(raw string) (string, error) {
	pkgName := strings.TrimSpace(raw)
	if pkgName == "" {
		return "", badRequest("pkgName required")
	}
	return pkgName, nil
}

// validateRepos fail-closes the repos replacement body:
//   - Repos must be present (a non-nil JSON array); null/missing → 400;
//   - an empty array is ALLOWED (clears all repos — the owner's explicit call);
//   - each entry is trimmed, must be non-blank, and must be an absolute http(s)
//     URL with a host (the same urlx.IsAbsoluteHTTP rule the FlareSolverr-URL
//     validator uses).
//
// It returns the trimmed list on success.
func validateRepos(req ReposUpdateRequest) ([]string, error) {
	if req.Repos == nil {
		return nil, badRequest("repos must be a JSON array")
	}
	out := make([]string, 0, len(req.Repos))
	for _, raw := range req.Repos {
		repo := strings.TrimSpace(raw)
		if repo == "" {
			return nil, badRequest("repos must not contain a blank URL")
		}
		if !urlx.IsAbsoluteHTTP(repo) {
			return nil, badRequest("repos must contain only absolute http(s) URLs")
		}
		out = append(out, repo)
	}
	return out, nil
}

// badRequest builds a 400 echo.HTTPError with the given message (surfaced
// verbatim by the central error middleware as {"message": …}).
func badRequest(msg string) error {
	return echo.NewHTTPError(http.StatusBadRequest, msg)
}
