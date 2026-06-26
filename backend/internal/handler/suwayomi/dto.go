package suwayomi

import suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"

// SuwayomiSettingsDTO is the JSON shape returned by GET/PATCH /api/suwayomi/settings.
// It groups the proxied subset into a FlareSolverr block and a SOCKS-proxy block
// (camelCase), mirroring how Suwayomi's own UI presents them.
type SuwayomiSettingsDTO struct {
	// FlareSolverr holds the Cloudflare-bypass proxy settings.
	FlareSolverr FlareSolverrDTO `json:"flareSolverr"`
	// SocksProxy holds the SOCKS-proxy settings.
	SocksProxy SocksProxyDTO `json:"socksProxy"`
}

// FlareSolverrDTO is the FlareSolverr settings group.
type FlareSolverrDTO struct {
	// Enabled toggles the FlareSolverr proxy.
	Enabled bool `json:"enabled"`
	// URL is the FlareSolverr endpoint (e.g. http://host:8191).
	URL string `json:"url"`
	// Timeout is the per-request timeout in seconds.
	Timeout int `json:"timeout"`
	// SessionName is the FlareSolverr session identifier.
	SessionName string `json:"sessionName"`
	// SessionTTL is the session time-to-live in minutes.
	SessionTTL int `json:"sessionTtl"`
	// AsResponseFallback uses FlareSolverr only as a fallback for blocked requests.
	AsResponseFallback bool `json:"asResponseFallback"`
}

// SocksProxyDTO is the SOCKS-proxy settings group. Port is a string because
// Suwayomi types it as a String on the wire (validated as a numeric port).
type SocksProxyDTO struct {
	// Enabled toggles routing source traffic through the SOCKS proxy.
	Enabled bool `json:"enabled"`
	// Version is the SOCKS protocol version (4 or 5).
	Version int `json:"version"`
	// Host is the proxy hostname or IP.
	Host string `json:"host"`
	// Port is the proxy port (numeric string).
	Port string `json:"port"`
	// Username is the optional proxy username.
	Username string `json:"username"`
	// Password is the optional proxy password.
	Password string `json:"password"`
}

// toDTO maps the client's SuwayomiSettings into the grouped HTTP DTO.
func toDTO(s suwayomicli.SuwayomiSettings) SuwayomiSettingsDTO {
	return SuwayomiSettingsDTO{
		FlareSolverr: FlareSolverrDTO{
			Enabled:            s.FlareSolverrEnabled,
			URL:                s.FlareSolverrURL,
			Timeout:            s.FlareSolverrTimeout,
			SessionName:        s.FlareSolverrSessionName,
			SessionTTL:         s.FlareSolverrSessionTTL,
			AsResponseFallback: s.FlareSolverrAsResponseFallback,
		},
		SocksProxy: SocksProxyDTO{
			Enabled:  s.SocksProxyEnabled,
			Version:  s.SocksProxyVersion,
			Host:     s.SocksProxyHost,
			Port:     s.SocksProxyPort,
			Username: s.SocksProxyUsername,
			Password: s.SocksProxyPassword,
		},
	}
}
