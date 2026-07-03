package imports

import (
	"fmt"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/handler/coverproxy"
)

// MangaCover handles GET /api/sources/:sourceId/manga/:mangaId/cover (B2).
//
// It proxies Suwayomi's own REST thumbnail endpoint
// (/api/v1/manga/{mangaId}/thumbnail) so Discover/Search cards can load a
// same-origin, authed cover image instead of resolving Suwayomi's raw
// (Suwayomi-relative) thumbnailUrl against Tsundoku's own origin — which
// 404s, since that path only exists on the Suwayomi server. :sourceId is
// accepted for route symmetry with the sibling /sources/:sourceId/... routes;
// Suwayomi's REST thumbnail path is keyed on mangaId alone, so it is not used
// to build coverURL. A non-integer :mangaId yields 400; a Suwayomi fetch
// failure maps to 502 via the shared coverproxy.Stream helper (mirrors
// handler/series's cover proxies).
func (h *Handler) MangaCover(c echo.Context) error {
	mangaID, err := parseMangaID(c.Param("mangaId"))
	if err != nil {
		return err
	}
	coverURL := fmt.Sprintf("/api/v1/manga/%d/thumbnail", mangaID)
	return coverproxy.Stream(c, h.sw, coverURL)
}
