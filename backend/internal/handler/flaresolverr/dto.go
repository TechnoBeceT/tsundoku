package flaresolverr

import (
	"context"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// SettingsDTO is the JSON shape returned by GET/PATCH /api/flaresolverr/settings.
// Field names deliberately MIRROR the retired Suwayomi settings-proxy's
// FlareSolverr group — same shape, same OpenAPI schema (FlareSolverrSettings)
// — so the frontend card that used to bind to the proxy can rebind here with
// only its data-layer composable changing.
type SettingsDTO struct {
	// Enabled toggles Tsundoku's own use of FlareSolverr.
	Enabled bool `json:"enabled"`
	// URL is the FlareSolverr endpoint (e.g. http://host:8191); "" = not configured.
	URL string `json:"url"`
	// Timeout is the per-request solve timeout in seconds.
	Timeout int `json:"timeout"`
	// SessionName is the FlareSolverr session identifier.
	SessionName string `json:"sessionName"`
	// SessionTTL is the session time-to-live in minutes.
	SessionTTL int `json:"sessionTtl"`
	// AsResponseFallback mirrors Suwayomi's own asResponseFallback flag.
	AsResponseFallback bool `json:"asResponseFallback"`
}

// currentDTO reads the six FlareSolverr settings from svc and assembles the
// response DTO — the single place both Get and Update (for its §16
// round-trip) build this shape from.
func currentDTO(ctx context.Context, svc *settingssvc.Service) SettingsDTO {
	return SettingsDTO{
		Enabled:            svc.FlareSolverrEnabled(ctx),
		URL:                svc.FlareSolverrURL(ctx),
		Timeout:            svc.FlareSolverrTimeout(ctx),
		SessionName:        svc.FlareSolverrSessionName(ctx),
		SessionTTL:         svc.FlareSolverrSessionTTL(ctx),
		AsResponseFallback: svc.FlareSolverrResponseFallback(ctx),
	}
}
