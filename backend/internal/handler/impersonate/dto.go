package impersonate

import (
	"context"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// SettingsDTO is the JSON shape returned by GET/PUT /api/impersonate — the two
// Tsundoku-owned impersonate-gateway values (the Chrome-fingerprint image-fetch
// gateway, GAP-111). It mirrors the FlareSolverr settings handler's shape (a
// flat DTO built from settings.Service accessors), just narrower.
type SettingsDTO struct {
	// Enabled toggles routing engine image fetches through the impersonate gateway.
	Enabled bool `json:"enabled"`
	// URL is the impersonate-gateway endpoint (e.g. http://impersonate-gateway:8788);
	// "" = not configured (disables it regardless of Enabled).
	URL string `json:"url"`
}

// currentDTO reads the two impersonate settings from svc and assembles the
// response DTO — the single place both Get and Update (for its §16 round-trip)
// build this shape from.
func currentDTO(ctx context.Context, svc *settingssvc.Service) SettingsDTO {
	return SettingsDTO{
		Enabled: svc.ImpersonateEnabled(ctx),
		URL:     svc.ImpersonateURL(ctx),
	}
}
