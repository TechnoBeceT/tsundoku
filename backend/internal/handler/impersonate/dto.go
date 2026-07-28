package impersonate

import (
	"context"
	"strconv"

	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// SettingsDTO is the JSON shape returned by GET/PUT /api/impersonate — the
// Tsundoku-owned impersonate-gateway values (the Chrome-fingerprint image-fetch
// gateway, GAP-111, scoped per source by GAP-131). It mirrors the FlareSolverr
// settings handler's shape (a flat DTO built from settings.Service accessors),
// just narrower.
type SettingsDTO struct {
	// Enabled is the group's MASTER switch. It is necessary but NOT sufficient:
	// a fetch also needs a URL and the fetching source listed in SourceIDs.
	Enabled bool `json:"enabled"`
	// URL is the impersonate-gateway endpoint (e.g. http://impersonate-gateway:8788);
	// "" = not configured (disables it regardless of Enabled). Global — one
	// gateway serves every gated source.
	URL string `json:"url"`
	// SourceIDs is the set of sources allowed to use the gateway (GAP-131),
	// canonical: de-duplicated, ascending. Each id is a 64-bit engine-host source
	// id serialised as a STRING, matching the Source schema's own convention so a
	// JSON client cannot lose precision. Empty (the default) = no source uses the
	// gateway.
	SourceIDs []string `json:"sourceIds"`
}

// currentDTO reads the three impersonate settings from svc and assembles the
// response DTO — the single place both Get and Update (for its §16 round-trip)
// build this shape from.
func currentDTO(ctx context.Context, svc *settingssvc.Service) SettingsDTO {
	return SettingsDTO{
		Enabled:   svc.ImpersonateEnabled(ctx),
		URL:       svc.ImpersonateURL(ctx),
		SourceIDs: formatSourceIDs(svc.ImpersonateSources(ctx)),
	}
}

// formatSourceIDs renders the settings layer's []int64 gating set as the wire's
// []string, always non-nil so the JSON carries `[]` rather than `null` for the
// (common) empty set.
func formatSourceIDs(ids []int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}
