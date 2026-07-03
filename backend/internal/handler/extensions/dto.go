package extensions

import (
	"net/url"

	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// ExtensionDTO is the JSON shape returned by the extension-list and the three
// mutating extension endpoints. It mirrors suwayomi.Extension verbatim in
// camelCase; the install/nsfw/obsolete flags keep Suwayomi's isInstalled/isNsfw/
// isObsolete naming so the FE reads them unambiguously.
type ExtensionDTO struct {
	// PkgName is the extension's identity (used in the action route path).
	PkgName string `json:"pkgName"`
	// Name is the human-readable display name.
	Name string `json:"name"`
	// Lang is the BCP-47 language tag (e.g. "en", "all").
	Lang string `json:"lang"`
	// VersionName is the human-readable version string.
	VersionName string `json:"versionName"`
	// VersionCode is the integer version.
	VersionCode int `json:"versionCode"`
	// IconURL is the Tsundoku same-origin icon proxy path
	// ("/api/suwayomi/extensions/{pkgName}/icon"), NOT Suwayomi's own raw
	// (cross-origin) icon URL — the browser can't load that directly. See
	// Handler.Icon.
	IconURL string `json:"iconUrl"`
	// Repo is the source repo URL; "" when Suwayomi reports null.
	Repo string `json:"repo"`
	// IsInstalled reports whether the extension is currently installed.
	IsInstalled bool `json:"isInstalled"`
	// HasUpdate reports whether an installed extension has a newer version.
	HasUpdate bool `json:"hasUpdate"`
	// IsNsfw reports whether the extension is flagged not-safe-for-work.
	IsNsfw bool `json:"isNsfw"`
	// IsObsolete reports whether the extension is orphaned (no longer in any repo).
	IsObsolete bool `json:"isObsolete"`
}

// ExtensionReposDTO is the JSON shape returned by GET/PUT /api/suwayomi/extensions/repos.
type ExtensionReposDTO struct {
	// Repos is the configured extension repo URL list (never null — [] when empty).
	Repos []string `json:"repos"`
}

// toExtensionDTO maps one client Extension into the HTTP DTO. It is the SINGLE
// mapper every extension-returning endpoint routes through, so no field is ever
// dropped on one path but not another. IconURL is REWRITTEN to the Tsundoku
// proxy path (mirrors how B2/M4 rewrote thumbnailUrl) — the raw Suwayomi
// e.IconURL never reaches the client; Handler.Icon looks it back up by pkgName
// when the proxy path is requested.
func toExtensionDTO(e suwayomicli.Extension) ExtensionDTO {
	return ExtensionDTO{
		PkgName:     e.PkgName,
		Name:        e.Name,
		Lang:        e.Lang,
		VersionName: e.VersionName,
		VersionCode: e.VersionCode,
		IconURL:     iconProxyPath(e.PkgName),
		Repo:        e.Repo,
		IsInstalled: e.IsInstalled,
		HasUpdate:   e.HasUpdate,
		IsNsfw:      e.IsNsfw,
		IsObsolete:  e.IsObsolete,
	}
}

// iconProxyPath builds the same-origin icon proxy path for an extension
// identified by pkgName. pkgName is URL-path-escaped defensively even though
// real Suwayomi pkgNames are dotted Java-style identifiers with no reserved
// characters.
func iconProxyPath(pkgName string) string {
	return "/api/suwayomi/extensions/" + url.PathEscape(pkgName) + "/icon"
}

// toExtensionDTOs maps a slice of client Extensions through toExtensionDTO. The
// result is always non-nil so the JSON body is [] (not null) for an empty list.
func toExtensionDTOs(exts []suwayomicli.Extension) []ExtensionDTO {
	out := make([]ExtensionDTO, 0, len(exts))
	for _, e := range exts {
		out = append(out, toExtensionDTO(e))
	}
	return out
}

// toReposDTO wraps the repo URL list, normalising a nil slice to [] so the JSON
// body is an empty array rather than null.
func toReposDTO(repos []string) ExtensionReposDTO {
	if repos == nil {
		repos = []string{}
	}
	return ExtensionReposDTO{Repos: repos}
}
