package suwayomi

import "context"

// This file (settings.go) holds the server-global settings passthrough: the
// FlareSolverr (Cloudflare-bypass) + SOCKS-proxy subset of Suwayomi's
// SettingsType. It is a pure proxy — Tsundoku stores none of these values; they
// live on whichever Suwayomi the client targets (BaseURL(), embed or external).
//
// Shapes confirmed by live introspection against Suwayomi v2.2.2100:
//   - query  settings: SettingsType!  (every field NON_NULL on output)
//   - mutation setSettings(input: SetSettingsInput!): SetSettingsPayload!
//       SetSettingsInput.settings: PartialSettingsTypeInput!  (all fields nullable)
//   - socksProxyPort is a String! (NOT an Int) on the wire.
// The build-tagged e2e shape-test (TestShape5_ServerSettings) is the merge gate
// that re-proves these names + types against a real server.

// SuwayomiSettings is the FlareSolverr + SOCKS-proxy subset of Suwayomi's
// server-global settings as returned by the settings query. Every field is a
// concrete value because Suwayomi types them all NON_NULL on output.
type SuwayomiSettings struct {
	// FlareSolverrEnabled toggles the FlareSolverr Cloudflare-bypass proxy.
	FlareSolverrEnabled bool
	// FlareSolverrURL is the FlareSolverr endpoint (e.g. http://host:8191).
	FlareSolverrURL string
	// FlareSolverrTimeout is the per-request timeout in seconds.
	FlareSolverrTimeout int
	// FlareSolverrSessionName is the FlareSolverr session identifier.
	FlareSolverrSessionName string
	// FlareSolverrSessionTTL is the session time-to-live in minutes.
	FlareSolverrSessionTTL int
	// FlareSolverrAsResponseFallback uses FlareSolverr only as a fallback when a
	// normal request is blocked, rather than for every request.
	FlareSolverrAsResponseFallback bool

	// SocksProxyEnabled toggles routing source traffic through a SOCKS proxy.
	SocksProxyEnabled bool
	// SocksProxyVersion is the SOCKS protocol version (4 or 5).
	SocksProxyVersion int
	// SocksProxyHost is the proxy hostname or IP.
	SocksProxyHost string
	// SocksProxyPort is the proxy port. Suwayomi types this as a String on the
	// wire, so it is a string here (validated as a numeric port at the boundary).
	SocksProxyPort string
	// SocksProxyUsername is the optional proxy username.
	SocksProxyUsername string
	// SocksProxyPassword is the optional proxy password.
	SocksProxyPassword string
}

// SuwayomiSettingsPatch is a partial update of the FlareSolverr + SOCKS subset.
// Every field is a pointer: a nil pointer means "leave this setting untouched"
// so a patch never clobbers a field the caller did not set. Only the non-nil
// fields are serialised into the PartialSettingsTypeInput, and Suwayomi leaves
// every OTHER (non-subset) server setting unchanged because the partial input
// omits them entirely.
type SuwayomiSettingsPatch struct {
	// FlareSolverrEnabled, when non-nil, sets flareSolverrEnabled.
	FlareSolverrEnabled *bool
	// FlareSolverrURL, when non-nil, sets flareSolverrUrl.
	FlareSolverrURL *string
	// FlareSolverrTimeout, when non-nil, sets flareSolverrTimeout (seconds).
	FlareSolverrTimeout *int
	// FlareSolverrSessionName, when non-nil, sets flareSolverrSessionName.
	FlareSolverrSessionName *string
	// FlareSolverrSessionTTL, when non-nil, sets flareSolverrSessionTtl (minutes).
	FlareSolverrSessionTTL *int
	// FlareSolverrAsResponseFallback, when non-nil, sets flareSolverrAsResponseFallback.
	FlareSolverrAsResponseFallback *bool

	// SocksProxyEnabled, when non-nil, sets socksProxyEnabled.
	SocksProxyEnabled *bool
	// SocksProxyVersion, when non-nil, sets socksProxyVersion (4 or 5).
	SocksProxyVersion *int
	// SocksProxyHost, when non-nil, sets socksProxyHost.
	SocksProxyHost *string
	// SocksProxyPort, when non-nil, sets socksProxyPort (a numeric string).
	SocksProxyPort *string
	// SocksProxyUsername, when non-nil, sets socksProxyUsername.
	SocksProxyUsername *string
	// SocksProxyPassword, when non-nil, sets socksProxyPassword.
	SocksProxyPassword *string
}

// putVar inserts *v into m under key only when v is non-nil. It is the single
// gate that keeps a partial patch sparse: a nil field is never written, so it is
// never sent and thus never clobbered.
func putVar[T any](m map[string]any, key string, v *T) {
	if v != nil {
		m[key] = *v
	}
}

// settingsMap builds the PartialSettingsTypeInput map containing ONLY the
// patch's non-nil fields. An empty patch yields an empty map.
func (p SuwayomiSettingsPatch) settingsMap() map[string]any {
	s := map[string]any{}
	putVar(s, "flareSolverrEnabled", p.FlareSolverrEnabled)
	putVar(s, "flareSolverrUrl", p.FlareSolverrURL)
	putVar(s, "flareSolverrTimeout", p.FlareSolverrTimeout)
	putVar(s, "flareSolverrSessionName", p.FlareSolverrSessionName)
	putVar(s, "flareSolverrSessionTtl", p.FlareSolverrSessionTTL)
	putVar(s, "flareSolverrAsResponseFallback", p.FlareSolverrAsResponseFallback)
	putVar(s, "socksProxyEnabled", p.SocksProxyEnabled)
	putVar(s, "socksProxyVersion", p.SocksProxyVersion)
	putVar(s, "socksProxyHost", p.SocksProxyHost)
	putVar(s, "socksProxyPort", p.SocksProxyPort)
	putVar(s, "socksProxyUsername", p.SocksProxyUsername)
	putVar(s, "socksProxyPassword", p.SocksProxyPassword)
	return s
}

// IsEmpty reports whether the patch sets no fields. The handler uses it to
// fail-close an empty PATCH body (a no-op update is rejected with a 400).
func (p SuwayomiSettingsPatch) IsEmpty() bool {
	return len(p.settingsMap()) == 0
}

// variables builds the GraphQL variables map for the setSettings mutation,
// wrapping the sparse PartialSettingsTypeInput under the "settings" key. Keeping
// the input sparse is what guarantees no unset field — in or out of the subset —
// is clobbered.
func (p SuwayomiSettingsPatch) variables() map[string]any {
	return map[string]any{"settings": p.settingsMap()}
}

// gqlServerSettingsData is the typed shape of the `data` field for the settings
// query. The same node shape is reused for the setSettings payload read-back.
type gqlServerSettingsData struct {
	Settings gqlServerSettingsNode `json:"settings"`
}

// gqlServerSettingsNode is the FlareSolverr + SOCKS selection of SettingsType.
type gqlServerSettingsNode struct {
	FlareSolverrEnabled            bool   `json:"flareSolverrEnabled"`
	FlareSolverrURL                string `json:"flareSolverrUrl"`
	FlareSolverrTimeout            int    `json:"flareSolverrTimeout"`
	FlareSolverrSessionName        string `json:"flareSolverrSessionName"`
	FlareSolverrSessionTTL         int    `json:"flareSolverrSessionTtl"`
	FlareSolverrAsResponseFallback bool   `json:"flareSolverrAsResponseFallback"`
	SocksProxyEnabled              bool   `json:"socksProxyEnabled"`
	SocksProxyVersion              int    `json:"socksProxyVersion"`
	SocksProxyHost                 string `json:"socksProxyHost"`
	SocksProxyPort                 string `json:"socksProxyPort"`
	SocksProxyUsername             string `json:"socksProxyUsername"`
	SocksProxyPassword             string `json:"socksProxyPassword"`
}

// toSettings converts the GraphQL node into the exported SuwayomiSettings struct.
func (n gqlServerSettingsNode) toSettings() SuwayomiSettings {
	return SuwayomiSettings{
		FlareSolverrEnabled:            n.FlareSolverrEnabled,
		FlareSolverrURL:                n.FlareSolverrURL,
		FlareSolverrTimeout:            n.FlareSolverrTimeout,
		FlareSolverrSessionName:        n.FlareSolverrSessionName,
		FlareSolverrSessionTTL:         n.FlareSolverrSessionTTL,
		FlareSolverrAsResponseFallback: n.FlareSolverrAsResponseFallback,
		SocksProxyEnabled:              n.SocksProxyEnabled,
		SocksProxyVersion:              n.SocksProxyVersion,
		SocksProxyHost:                 n.SocksProxyHost,
		SocksProxyPort:                 n.SocksProxyPort,
		SocksProxyUsername:             n.SocksProxyUsername,
		SocksProxyPassword:             n.SocksProxyPassword,
	}
}

// serverSettingsSelection is the shared field selection (FlareSolverr + SOCKS)
// used by both the settings query and the setSettings payload read-back, so the
// two never drift.
const serverSettingsSelection = `
    flareSolverrEnabled
    flareSolverrUrl
    flareSolverrTimeout
    flareSolverrSessionName
    flareSolverrSessionTtl
    flareSolverrAsResponseFallback
    socksProxyEnabled
    socksProxyVersion
    socksProxyHost
    socksProxyPort
    socksProxyUsername
    socksProxyPassword`

// serverSettingsQuery selects only the FlareSolverr + SOCKS subset of
// SettingsType — the fields Tsundoku proxies.
const serverSettingsQuery = `
query ServerSettings {
  settings {` + serverSettingsSelection + `
  }
}`

// setServerSettingsMutation applies a partial update via setSettings. The
// $settings variable is a PartialSettingsTypeInput carrying only the changed
// fields; the payload is read back so the mutation selection is non-empty.
const setServerSettingsMutation = `
mutation SetServerSettings($settings: PartialSettingsTypeInput!) {
  setSettings(input: { settings: $settings }) {
    settings {` + serverSettingsSelection + `
    }
  }
}`

// ServerSettings fetches the FlareSolverr + SOCKS subset of Suwayomi's
// server-global settings via the settings GraphQL query. It returns the current
// values; a transport or GraphQL error is surfaced (never silently swallowed).
func (c *httpClient) ServerSettings(ctx context.Context) (SuwayomiSettings, error) {
	var data gqlServerSettingsData
	if err := c.doGraphQL(ctx, serverSettingsQuery, nil, &data); err != nil {
		return SuwayomiSettings{}, err
	}
	return data.Settings.toSettings(), nil
}

// SetServerSettings applies a PARTIAL update of the FlareSolverr + SOCKS subset
// via the setSettings mutation. Only the patch's non-nil fields are sent, so
// unset fields — including every server setting outside this subset — are left
// untouched. A transport or GraphQL error is surfaced.
func (c *httpClient) SetServerSettings(ctx context.Context, patch SuwayomiSettingsPatch) error {
	// out is nil: the payload read-back exists only to give the mutation a
	// non-empty selection; the caller re-reads via ServerSettings for the §16
	// round-trip so it observes the authoritative persisted state.
	return c.doGraphQL(ctx, setServerSettingsMutation, patch.variables(), nil)
}
