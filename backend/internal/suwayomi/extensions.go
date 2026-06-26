package suwayomi

import (
	"context"
	"fmt"
)

// This file (extensions.go) holds the Suwayomi extension-management passthrough:
// listing the Tachiyomi/Mihon source plugins (installed + available),
// installing / updating / uninstalling a repo extension, refreshing the
// available list from the configured repos, and reading/replacing the repo URL
// list. Like settings.go it is a PURE proxy — Tsundoku stores none of this; the
// extensions live entirely on whichever Suwayomi the client targets (BaseURL(),
// embed or external).
//
// Shapes confirmed by live introspection against Suwayomi v2.2.2100 (re-proven
// at merge by the build-tagged TestShape6_Extensions):
//   - query    extensions { nodes { <ExtensionType fields> } }
//   - mutation updateExtension(input: { id: String!, patch: UpdateExtensionPatchInput! })
//       patch = { install / update / uninstall : Boolean } — set EXACTLY one true.
//       NOTE: `id` is the pkgName STRING, not a numeric id.
//   - mutation fetchExtensions(input: {}) { extensions { … } }  — refresh from repos.
//   - field    settings.extensionRepos: [String!]!  (read) /
//              setSettings(input:{settings:{extensionRepos:[String!]}}) (replace).
//
// FOOTGUNS (do not "fix" the casing): the identity field is `pkgName` (there is
// NO `id` field on ExtensionType), and the booleans are `isInstalled` /
// `isObsolete` (NOT `installed` / `obsolete`).

// Extension is the Tsundoku-side view of a Suwayomi extension (a source plugin).
// Repo is "" when Suwayomi reports it as null (the only nullable wire field).
type Extension struct {
	// PkgName is the extension's package name — its stable IDENTITY (there is no
	// numeric id; updateExtension's `id` input takes this string).
	PkgName string
	// Name is the human-readable display name.
	Name string
	// Lang is the BCP-47 language tag the extension serves (e.g. "en", "all").
	Lang string
	// VersionName is the human-readable version string (e.g. "1.4.2").
	VersionName string
	// VersionCode is the monotonically increasing integer version.
	VersionCode int
	// IconURL is the raw Suwayomi icon URL (no Tsundoku proxy in v1).
	IconURL string
	// Repo is the source repo URL this extension came from; "" when null.
	Repo string
	// IsInstalled reports whether the extension is currently installed.
	IsInstalled bool
	// HasUpdate reports whether an installed extension has a newer version available.
	HasUpdate bool
	// IsNsfw reports whether the extension is flagged not-safe-for-work.
	IsNsfw bool
	// IsObsolete reports whether the extension is no longer present in any repo
	// (installed but orphaned).
	IsObsolete bool
}

// ExtensionAction selects which single boolean of updateExtension's patch to set
// true. The handler only ever passes the three constants below; an unknown value
// is a programmer error and is rejected rather than sending an empty patch.
type ExtensionAction string

const (
	// ExtensionInstall installs an available extension (patch.install = true).
	ExtensionInstall ExtensionAction = "install"
	// ExtensionUpdate updates an installed extension (patch.update = true).
	ExtensionUpdate ExtensionAction = "update"
	// ExtensionUninstall removes an installed extension (patch.uninstall = true).
	ExtensionUninstall ExtensionAction = "uninstall"
)

// extensionPatch builds the UpdateExtensionPatchInput map with EXACTLY the one
// boolean named by action set to true. It is the single patch builder shared by
// SetExtensionState, so install/update/uninstall are not triplicated. An unknown
// action returns an error (never an empty patch, which would be a silent no-op).
func extensionPatch(action ExtensionAction) (map[string]any, error) {
	switch action {
	case ExtensionInstall:
		return map[string]any{"install": true}, nil
	case ExtensionUpdate:
		return map[string]any{"update": true}, nil
	case ExtensionUninstall:
		return map[string]any{"uninstall": true}, nil
	default:
		return nil, fmt.Errorf("suwayomi: unknown extension action %q", action)
	}
}

// gqlExtensionNode is the ExtensionType selection used by both the extensions
// query and the fetchExtensions mutation read-back. Repo is *string because it
// is the only nullable field on the wire.
type gqlExtensionNode struct {
	PkgName     string  `json:"pkgName"`
	Name        string  `json:"name"`
	Lang        string  `json:"lang"`
	VersionName string  `json:"versionName"`
	VersionCode int     `json:"versionCode"`
	IconURL     string  `json:"iconUrl"`
	Repo        *string `json:"repo"`
	IsInstalled bool    `json:"isInstalled"`
	HasUpdate   bool    `json:"hasUpdate"`
	IsNsfw      bool    `json:"isNsfw"`
	IsObsolete  bool    `json:"isObsolete"`
}

// toExtension converts a GraphQL node into the exported Extension struct,
// collapsing a null repo to "".
func (n gqlExtensionNode) toExtension() Extension {
	repo := ""
	if n.Repo != nil {
		repo = *n.Repo
	}
	return Extension{
		PkgName:     n.PkgName,
		Name:        n.Name,
		Lang:        n.Lang,
		VersionName: n.VersionName,
		VersionCode: n.VersionCode,
		IconURL:     n.IconURL,
		Repo:        repo,
		IsInstalled: n.IsInstalled,
		HasUpdate:   n.HasUpdate,
		IsNsfw:      n.IsNsfw,
		IsObsolete:  n.IsObsolete,
	}
}

// mapExtensionNodes converts a slice of GraphQL nodes to []Extension.
func mapExtensionNodes(nodes []gqlExtensionNode) []Extension {
	out := make([]Extension, len(nodes))
	for i, n := range nodes {
		out[i] = n.toExtension()
	}
	return out
}

// extensionSelection is the shared ExtensionType field selection used by the
// extensions query and the fetchExtensions read-back, so the two never drift.
const extensionSelection = `
    pkgName
    name
    lang
    versionName
    versionCode
    iconUrl
    repo
    isInstalled
    hasUpdate
    isNsfw
    isObsolete`

// extensionsQuery lists all extensions (installed + available) as a node list.
const extensionsQuery = `
query Extensions {
  extensions {
    nodes {` + extensionSelection + `
    }
  }
}`

// updateExtensionMutation installs/updates/uninstalls a single extension. The
// $patch carries exactly one of install/update/uninstall (see extensionPatch).
// `id` is the pkgName string. Only clientMutationId is selected because the
// caller re-reads via Extensions for the §16 round-trip.
const updateExtensionMutation = `
mutation UpdateExtension($id: String!, $patch: UpdateExtensionPatchInput!) {
  updateExtension(input: { id: $id, patch: $patch }) {
    clientMutationId
  }
}`

// fetchExtensionsMutation refreshes the available-extensions list from the
// configured repos ("check for updates") and returns the refreshed list.
const fetchExtensionsMutation = `
mutation FetchExtensions {
  fetchExtensions(input: {}) {
    extensions {` + extensionSelection + `
    }
  }
}`

// extensionReposQuery reads the repo URL list (a plain SettingsType field). It is
// a SEPARATE, minimal selection from serverSettingsSelection (settings.go) so the
// FlareSolverr/SOCKS subset is not widened.
const extensionReposQuery = `
query ExtensionRepos {
  settings {
    extensionRepos
  }
}`

// setExtensionReposMutation REPLACES the repo URL list via a partial setSettings
// input carrying ONLY extensionRepos, so every other server setting is untouched.
const setExtensionReposMutation = `
mutation SetExtensionRepos($repos: [String!]) {
  setSettings(input: { settings: { extensionRepos: $repos } }) {
    settings {
      extensionRepos
    }
  }
}`

// gqlExtensionsData is the typed `data` shape for the extensions query.
type gqlExtensionsData struct {
	Extensions struct {
		Nodes []gqlExtensionNode `json:"nodes"`
	} `json:"extensions"`
}

// gqlFetchExtensionsData is the typed `data` shape for the fetchExtensions mutation.
type gqlFetchExtensionsData struct {
	FetchExtensions struct {
		Extensions []gqlExtensionNode `json:"extensions"`
	} `json:"fetchExtensions"`
}

// gqlExtensionReposData is the typed `data` shape for the extensionRepos query.
type gqlExtensionReposData struct {
	Settings struct {
		ExtensionRepos []string `json:"extensionRepos"`
	} `json:"settings"`
}

// Extensions lists every Suwayomi extension (installed + available) via the
// extensions GraphQL query. A transport or GraphQL error is surfaced (never
// silently swallowed); an empty list (no repos configured) is a valid result.
func (c *httpClient) Extensions(ctx context.Context) ([]Extension, error) {
	var data gqlExtensionsData
	if err := c.doGraphQL(ctx, extensionsQuery, nil, &data); err != nil {
		return nil, err
	}
	return mapExtensionNodes(data.Extensions.Nodes), nil
}

// SetExtensionState installs, updates, or uninstalls the extension identified by
// pkgName (the extension's identity) via the updateExtension mutation. action
// selects which single patch boolean is set true; an unknown action is rejected
// before any request is made. The caller re-reads via Extensions for the §16
// round-trip, so this returns only success/failure.
func (c *httpClient) SetExtensionState(ctx context.Context, pkgName string, action ExtensionAction) error {
	patch, err := extensionPatch(action)
	if err != nil {
		return err
	}
	vars := map[string]any{"id": pkgName, "patch": patch}
	return c.doGraphQL(ctx, updateExtensionMutation, vars, nil)
}

// FetchExtensions refreshes the available-extensions list from the configured
// repos (the "check for updates" action) via the fetchExtensions mutation and
// returns the refreshed list. A transport or GraphQL error is surfaced.
func (c *httpClient) FetchExtensions(ctx context.Context) ([]Extension, error) {
	var data gqlFetchExtensionsData
	if err := c.doGraphQL(ctx, fetchExtensionsMutation, nil, &data); err != nil {
		return nil, err
	}
	return mapExtensionNodes(data.FetchExtensions.Extensions), nil
}

// ExtensionRepos reads the configured extension repo URL list via the settings
// query. A transport or GraphQL error is surfaced.
func (c *httpClient) ExtensionRepos(ctx context.Context) ([]string, error) {
	var data gqlExtensionReposData
	if err := c.doGraphQL(ctx, extensionReposQuery, nil, &data); err != nil {
		return nil, err
	}
	return data.Settings.ExtensionRepos, nil
}

// SetExtensionRepos REPLACES the extension repo URL list via a partial
// setSettings mutation that sends only extensionRepos, so no other server
// setting is clobbered. An empty slice clears all repos (replace semantics). The
// caller re-reads via ExtensionRepos for the §16 round-trip.
func (c *httpClient) SetExtensionRepos(ctx context.Context, repos []string) error {
	vars := map[string]any{"repos": repos}
	return c.doGraphQL(ctx, setExtensionReposMutation, vars, nil)
}
