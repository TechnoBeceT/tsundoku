package enginetopo

import (
	"context"
	"fmt"
	"strconv"

	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// SettingsWriter is the minimal write surface SeedEngineConfig needs from the
// runtime settings overlay — narrowed so a test double never has to implement
// the whole settings.Service. *settings.Service satisfies it directly via its
// existing SetMany method.
type SettingsWriter interface {
	SetMany(ctx context.Context, updates []settings.KeyValue) error
}

// SeedEngineConfig reads the live engine's FlareSolverr + SOCKS server
// settings (suwayomi.Client.ServerSettings) and writes them into Tsundoku's
// own settings overlay: the FlareSolverr values into the EXISTING
// flaresolverr.* keys (QCAT-238 already established FlareSolverr as a
// Tsundoku-owned setting — this reuses that allowlist rather than
// duplicating it), and the SOCKS values into the NEW engine.socks_* keys.
//
// FlareSolverr and SOCKS are seeded as TWO INDEPENDENT batches, deliberately:
// SOCKS is opt-in and unconfigured on most installs — a stock Suwayomi reports
// an EMPTY socksProxyPort, which the engine.socks_port tunable (bounded
// 1..65535) rejects. A single all-or-nothing batch would let that rejected
// SOCKS value sink the FlareSolverr write as collateral, so the first real seed
// against a normal Suwayomi would silently discard FlareSolverr. Keeping them
// separate guarantees a SOCKS problem can never prevent FlareSolverr from being
// seeded.
//
// SOCKS is moreover SKIPPED entirely when it is off — SOCKS disabled OR a blank
// port means "nothing configured to seed" (mirrors how encodePreferenceValue
// skips an absent preference value); we never substitute a fake default port
// for a disabled proxy. When SOCKS carries a real value it is seeded and, if
// that value is somehow out of range, SetMany's own validation surfaces the
// error (a genuine misconfiguration worth reporting).
func SeedEngineConfig(ctx context.Context, client suwayomi.Client, w SettingsWriter) error {
	live, err := client.ServerSettings(ctx)
	if err != nil {
		return fmt.Errorf("enginetopo.SeedEngineConfig: ServerSettings: %w", err)
	}

	if err := w.SetMany(ctx, flareSolverrUpdates(live)); err != nil {
		return fmt.Errorf("enginetopo.SeedEngineConfig: seed flaresolverr: %w", err)
	}

	if socks := socksUpdates(live); socks != nil {
		if err := w.SetMany(ctx, socks); err != nil {
			return fmt.Errorf("enginetopo.SeedEngineConfig: seed socks: %w", err)
		}
	}
	return nil
}

// flareSolverrUpdates maps the engine's FlareSolverr settings onto the existing
// Tsundoku flaresolverr.* tunable keys (QCAT-238). These fields are all
// NON_NULL on Suwayomi's wire, so the batch always carries a valid value.
func flareSolverrUpdates(live suwayomi.SuwayomiSettings) []settings.KeyValue {
	return []settings.KeyValue{
		{Key: settings.KeyFlareSolverrEnabled, Value: strconv.FormatBool(live.FlareSolverrEnabled)},
		{Key: settings.KeyFlareSolverrURL, Value: live.FlareSolverrURL},
		{Key: settings.KeyFlareSolverrTimeout, Value: strconv.Itoa(live.FlareSolverrTimeout)},
		{Key: settings.KeyFlareSolverrSessionName, Value: live.FlareSolverrSessionName},
		{Key: settings.KeyFlareSolverrSessionTTL, Value: strconv.Itoa(live.FlareSolverrSessionTTL)},
		{Key: settings.KeyFlareSolverrResponseFallback, Value: strconv.FormatBool(live.FlareSolverrAsResponseFallback)},
	}
}

// socksUpdates maps the engine's SOCKS settings onto the engine.socks_* tunable
// keys, or returns nil when SOCKS is off — disabled OR a blank port means there
// is nothing configured to seed (a stock Suwayomi reports an empty
// socksProxyPort). SocksProxyPort is already a numeric string on Suwayomi's own
// wire (see suwayomi.SuwayomiSettings.SocksProxyPort's doc comment), so a
// non-blank value passes straight through to the int tunable's validator.
func socksUpdates(live suwayomi.SuwayomiSettings) []settings.KeyValue {
	if !live.SocksProxyEnabled || live.SocksProxyPort == "" {
		return nil
	}
	return []settings.KeyValue{
		{Key: settings.KeyEngineSocksEnabled, Value: strconv.FormatBool(live.SocksProxyEnabled)},
		{Key: settings.KeyEngineSocksHost, Value: live.SocksProxyHost},
		{Key: settings.KeyEngineSocksPort, Value: live.SocksProxyPort},
		{Key: settings.KeyEngineSocksVersion, Value: strconv.Itoa(live.SocksProxyVersion)},
		// SocksProxyUsername / SocksProxyPassword are DELIBERATELY OMITTED: the
		// generic Settings.value column is NOT .Sensitive() and IS exposed
		// verbatim via GET /api/settings, so a SOCKS credential must never become
		// a tunable (contrast SourcePreference.value, which IS .Sensitive() and
		// is the only sanctioned home for a seeded secret).
	}
}
