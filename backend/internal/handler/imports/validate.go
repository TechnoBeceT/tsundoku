// Package imports contains the thin HTTP handlers for the imports API: listing
// sources, searching across sources, inspecting a manga's chapters, and adopting
// a manga (or a group of manga from multiple sources) into the library.
// Business logic lives in internal/imports (the service); these handlers only
// bind/parse the request, validate it, call the service, and render the DTO.
package imports

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/imports"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// cgnatRange is the Carrier-Grade NAT block (RFC 6598, 100.64.0.0/10) — ISPs
// route it internally for NAT44 and it is never publicly reachable, but it is
// NOT covered by any net.IP predicate (IsPrivate/IsLoopback/IsLinkLocal all
// miss it), so isPublicHTTPHost checks it explicitly. Parsed once at package
// init from a literal that is always valid — a parse failure here would be a
// typo in this file, not a runtime condition, so it panics loudly instead of
// silently disabling the check.
var cgnatRange = func() *net.IPNet {
	_, ipNet, err := net.ParseCIDR("100.64.0.0/10")
	if err != nil {
		panic("imports: invalid CGNAT CIDR literal: " + err.Error())
	}
	return ipNet
}()

// adoptProviderRequest is the per-provider element within AdoptRequestBody. It
// mirrors imports.AdoptProvider with JSON tags for camelCase wire format.
//
// MangaID + URL (P2 Suwayomi-removal, slice 3b): MangaID is KEPT, additive
// only, so the not-yet-updated frontend still typechecks; the backend reads
// URL (the source-relative manga URL the engine host addresses this manga
// by) instead.
type adoptProviderRequest struct {
	// Source is the engine-host source ID, stringified.
	Source string `json:"source"`
	// MangaID is UNUSED by the backend — retained for FE wire compatibility
	// only (prefer URL).
	MangaID int `json:"mangaId"`
	// URL is the source-relative manga URL.
	URL string `json:"url"`
	// Importance is the provider rank (higher = preferred); must be >= 0.
	Importance int `json:"importance"`
	// Scanlator selects which scanlation group's chapters this provider
	// tracks; optional, "" means "all chapters from this source".
	Scanlator string `json:"scanlator"`
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

// parseSourceID parses the :sourceId path param as a decimal int64 — the
// engine-host source identity coverproxy.StreamEngine (and the underlying
// sourceengine.Client.Image call) addresses a source by. A blank or
// non-numeric value yields a 400 echo.HTTPError.
func parseSourceID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	return id, nil
}

// parseCoverURL validates the REQUIRED ?url query param on SourceCover — the
// raw thumbnail/cover URL (as returned by Search/Browse's SearchCandidate DTO)
// the engine host is asked to re-fetch. Kept separate from parseChapterURL
// (same non-empty rule) because the two params address conceptually different
// things (a manga page URL vs. an image URL) and a shared name would blur
// that at the call site.
//
// SSRF HARDENING: this endpoint sits behind mw.RequireOwner, but a cross-site
// request (e.g. an <img> tag on another page, riding the owner's cookie
// session) can still DRIVE it — the handler would otherwise fetch whatever
// host the caller names, including internal/loopback/metadata targets. A
// legitimate source cover is ALWAYS a public CDN, so ?url is restricted to an
// absolute http(s) URL whose host is not internal (see isPublicHTTPHost for
// the exact rules + the documented residual). Any rejection is a 400
// echo.HTTPError with a single generic message — it does not distinguish
// "not http(s)" from "internal host" so a probing caller learns nothing about
// which check tripped.
func parseCoverURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "url is required")
	}
	if !urlx.IsAbsoluteHTTP(u) {
		return "", echo.NewHTTPError(http.StatusBadRequest, "url must be a valid public http(s) URL")
	}
	parsed, err := url.Parse(u)
	if err != nil || !isPublicHTTPHost(parsed.Hostname()) {
		return "", echo.NewHTTPError(http.StatusBadRequest, "url must be a valid public http(s) URL")
	}
	return u, nil
}

// isPublicHTTPHost reports whether host (already extracted via
// url.URL.Hostname(), so it carries no port and IPv6 brackets are stripped)
// is safe for the engine host to fetch on the owner's behalf as a source
// cover — i.e. NOT localhost, an internal/private IP, link-local (incl. the
// 169.254.169.254 cloud-metadata address, covered by IsLinkLocalUnicast), or
// Carrier-Grade NAT (100.64.0.0/10, checked separately — see cgnatRange).
// This is the extracted SSRF guard behind parseCoverURL.
//
// KNOWN, ACCEPTED RESIDUAL: this validates the host LITERAL only. A public
// hostname that DNS-resolves to a private IP (DNS rebinding) is NOT caught
// here — the actual fetch happens engine-side (Kotlin), which does its own
// resolution, so closing that gap needs a connection-time safe dialer there
// (out of scope). Accepted under the single-owner homelab threat model: the
// realistic threat this hardens against is a cross-site request riding the
// owner's session to probe THIS server's network, not a DNS-rebinding
// adversary who already controls a domain the owner would load.
func isPublicHTTPHost(host string) bool {
	if host == "" {
		return false
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Not an IP literal — a plain public hostname (theblank.net, etc.).
		// DNS resolution happens engine-side; see the residual note above.
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	return !cgnatRange.Contains(ip)
}

// parseBrowseType maps the ?type query parameter to an imports.BrowseType.
// "popular" → POPULAR, "latest" → LATEST; any other value (including empty)
// yields a 400 echo.HTTPError — type is a required closed enum.
func parseBrowseType(raw string) (imports.BrowseType, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "popular":
		return imports.BrowsePopular, nil
	case "latest":
		return imports.BrowseLatest, nil
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

// parseChapterURL validates a REQUIRED ?url query param used by the
// preview endpoints that now dispatch through the URL-addressed backend
// (InspectChapters/Details/Breakdown — P2 Suwayomi-removal, slice 3b). The
// :mangaId path segment is kept (route unchanged, for FE compat) but is no
// longer parsed/validated here — it is bound and ignored (see each handler's
// doc comment). This transition is deliberately RUNTIME-broken against the
// not-yet-updated frontend (which sends no ?url=) until slice 3b-FE lands;
// a missing url is a clean 400 rather than a silent wrong-manga fetch.
func parseChapterURL(raw string) (string, error) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, "url is required")
	}
	return u, nil
}

// validateAdoptBody validates the parsed AdoptRequestBody:
//   - title must be non-blank.
//   - providers must have >= 1 entry.
//   - each provider's importance must be >= 0.
//   - each provider's url must be non-blank (P2 Suwayomi-removal — the
//     backend is URL-addressed; see adoptProviderRequest's doc comment).
//   - each (source, scanlator) pair must be distinct across providers (a
//     series may carry at most one provider per (source, scanlator) —
//     duplicates would silently collapse onto a single SeriesProvider row).
//     The SAME source MAY appear more than once under DIFFERENT scanlators
//     (e.g. adopting one aggregator source split into two scanlation groups
//     with independent importances) — see setImportances, which matches by
//     the full (series, provider, scanlator) triple.
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
		if strings.TrimSpace(p.URL) == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "provider url is required")
		}
		key := p.Source + "\x00" + p.Scanlator
		if seen[key] {
			return echo.NewHTTPError(http.StatusBadRequest, "duplicate source+scanlator in providers: each (source, scanlator) pair may appear at most once")
		}
		seen[key] = true
	}
	if req.Category != "" {
		if _, err := category.ValidateName(req.Category); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid category: "+req.Category)
		}
	}
	return nil
}
