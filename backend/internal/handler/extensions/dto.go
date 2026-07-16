package extensions

import (
	"strconv"

	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// ExtensionDTO is the JSON shape returned by the extension-list and the three
// mutating extension endpoints. It mirrors sourceengine.Extension verbatim in
// camelCase; the install/nsfw flags keep the engine host's isInstalled/isNsfw
// naming so the FE reads them unambiguously.
//
// (QCAT-253, P2 Suwayomi-removal slice 5): iconUrl is now the engine host's OWN
// reported icon URL, served DIRECTLY (the retired Suwayomi icon proxy is gone —
// sourceengine has no PageBytes-shaped fetch to stream it through; the FE
// renders iconUrl as-is). isObsolete is gone too — sourceengine.Extension has
// no such flag. sources is NEW: the extension's content sources are embedded on
// the wire (sourceengine.Extension.Sources), so the per-source preferences
// endpoint (preferences.go) no longer needs a separate lookup call.
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
	VersionCode int64 `json:"versionCode"`
	// IconURL is the engine host's own reported icon image URL, served as-is
	// (not proxied — see the type doc comment).
	IconURL string `json:"iconUrl"`
	// RepoURL is the configured repo this extension was resolved from; null
	// when the extension is not associated with a repo (e.g. sideloaded).
	RepoURL *string `json:"repoUrl"`
	// IsInstalled reports whether the extension is currently installed.
	IsInstalled bool `json:"isInstalled"`
	// HasUpdate reports whether an installed extension has a newer version.
	HasUpdate bool `json:"hasUpdate"`
	// IsNsfw reports whether the extension is flagged not-safe-for-work.
	IsNsfw bool `json:"isNsfw"`
	// Sources lists the content sources this extension provides (one per
	// language it supports) — the set the per-source preferences endpoint
	// (GET .../preferences) resolves against.
	Sources []ExtensionSourceDTO `json:"sources"`
}

// ExtensionSourceDTO is one content source an extension provides, mirroring
// sourceengine.Source. ID is rendered as a decimal STRING (not a JSON number)
// — the same wire convention every other source-id-carrying DTO in this API
// uses (e.g. imports.SourceDTO), so a 64-bit id is never silently truncated by
// a JS number.
type ExtensionSourceDTO struct {
	// ID is the source's stable numeric identifier, as a decimal string.
	ID string `json:"id"`
	// Name is the human-readable source name.
	Name string `json:"name"`
	// Lang is the BCP-47 language tag the source reports.
	Lang string `json:"lang"`
}

// ExtensionReposDTO is the JSON shape returned by GET/PUT /api/suwayomi/extensions/repos.
type ExtensionReposDTO struct {
	// Repos is the configured extension repo URL list (never null — [] when empty).
	Repos []string `json:"repos"`
}

// toExtensionDTO maps one client Extension into the HTTP DTO. It is the SINGLE
// mapper every extension-returning endpoint routes through, so no field is ever
// dropped on one path but not another.
func toExtensionDTO(e sourceengine.Extension) ExtensionDTO {
	return ExtensionDTO{
		PkgName:     e.PkgName,
		Name:        e.Name,
		Lang:        e.Lang,
		VersionName: e.VersionName,
		VersionCode: e.VersionCode,
		IconURL:     e.IconURL,
		RepoURL:     e.RepoURL,
		IsInstalled: e.IsInstalled,
		HasUpdate:   e.HasUpdate,
		IsNsfw:      e.IsNsfw,
		Sources:     toExtensionSourceDTOs(e.Sources),
	}
}

// formatSourceID renders an engine-host numeric source id as the wire/DTO
// decimal-string form — shared by every source-id-carrying DTO in this
// package (ExtensionSourceDTO, SourcePreferencesGroupDTO) so the convention
// lives in one place.
func formatSourceID(id int64) string {
	return strconv.FormatInt(id, 10)
}

// toExtensionSourceDTO maps one sourceengine.Source into its DTO, stringifying
// the id (see ExtensionSourceDTO's doc comment).
func toExtensionSourceDTO(s sourceengine.Source) ExtensionSourceDTO {
	return ExtensionSourceDTO{
		ID:   formatSourceID(s.ID),
		Name: s.Name,
		Lang: s.Lang,
	}
}

// toExtensionSourceDTOs maps a slice of sourceengine.Source through the single
// mapper. The result is always non-nil so an empty list serialises to [] (not
// null).
func toExtensionSourceDTOs(sources []sourceengine.Source) []ExtensionSourceDTO {
	out := make([]ExtensionSourceDTO, 0, len(sources))
	for _, s := range sources {
		out = append(out, toExtensionSourceDTO(s))
	}
	return out
}

// toExtensionDTOs maps a slice of client Extensions through toExtensionDTO. The
// result is always non-nil so the JSON body is [] (not null) for an empty list.
func toExtensionDTOs(exts []sourceengine.Extension) []ExtensionDTO {
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
