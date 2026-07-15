package enginetopo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/enginetopo"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// capturingWriter is a settings.SettingsWriter test double that records the
// KeyValue batches it was asked to persist (ACCUMULATED across calls, because
// SeedEngineConfig now writes FlareSolverr and SOCKS as two separate batches),
// instead of touching a real DB — the mapping logic (ServerSettings → keys) is
// what these tests exercise; the real-Service round-trip is covered by the
// testdb-backed tests below.
type capturingWriter struct {
	got   []settings.KeyValue
	calls int
	err   error
	errOn int // when >0, fail SetMany only on the errOn-th call (1-based)
}

func (w *capturingWriter) SetMany(_ context.Context, updates []settings.KeyValue) error {
	w.calls++
	if w.err != nil && (w.errOn == 0 || w.errOn == w.calls) {
		return w.err
	}
	w.got = append(w.got, updates...)
	return nil
}

func (w *capturingWriter) value(key string) (string, bool) {
	for _, kv := range w.got {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return "", false
}

// TestSeedEngineConfig_WritesFlareSolverrAndSocksKeys proves a live
// ServerSettings response is mapped onto the expected FlareSolverr (existing,
// QCAT-238) + SOCKS (new) settings keys with the correct string encoding.
func TestSeedEngineConfig_WritesFlareSolverrAndSocksKeys(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		FlareSolverrEnabled:            true,
		FlareSolverrURL:                "http://flaresolverr.internal:8191",
		FlareSolverrTimeout:            90,
		FlareSolverrSessionName:        "tsundoku",
		FlareSolverrSessionTTL:         30,
		FlareSolverrAsResponseFallback: true,
		SocksProxyEnabled:              true,
		SocksProxyVersion:              5,
		SocksProxyHost:                 "socks.internal",
		SocksProxyPort:                 "1080",
		SocksProxyUsername:             "proxyuser",
		SocksProxyPassword:             "proxypass",
	}}
	w := &capturingWriter{}

	if err := enginetopo.SeedEngineConfig(ctx, fc, w); err != nil {
		t.Fatalf("SeedEngineConfig: %v", err)
	}

	cases := []struct {
		key  string
		want string
	}{
		{settings.KeyFlareSolverrEnabled, "true"},
		{settings.KeyFlareSolverrURL, "http://flaresolverr.internal:8191"},
		{settings.KeyFlareSolverrTimeout, "90"},
		{settings.KeyFlareSolverrSessionName, "tsundoku"},
		{settings.KeyFlareSolverrSessionTTL, "30"},
		{settings.KeyFlareSolverrResponseFallback, "true"},
		{settings.KeyEngineSocksEnabled, "true"},
		{settings.KeyEngineSocksHost, "socks.internal"},
		{settings.KeyEngineSocksPort, "1080"},
		{settings.KeyEngineSocksVersion, "5"},
	}
	for _, tc := range cases {
		got, ok := w.value(tc.key)
		if !ok {
			t.Errorf("missing key %q in the written batch", tc.key)
			continue
		}
		if got != tc.want {
			t.Errorf("key %q = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestSeedEngineConfig_ServerSettingsErrorPropagates proves a transport/
// GraphQL failure from the engine surfaces as an error and never calls the
// writer.
func TestSeedEngineConfig_ServerSettingsErrorPropagates(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{serverSettingsErr: errors.New("engine unreachable")}
	w := &capturingWriter{}

	err := enginetopo.SeedEngineConfig(ctx, fc, w)
	if err == nil {
		t.Fatal("SeedEngineConfig: want error, got nil")
	}
	if w.got != nil {
		t.Error("SetMany must not be called when ServerSettings fails")
	}
}

// TestSeedEngineConfig_SetManyErrorPropagates proves a settings-layer
// validation/persistence failure surfaces to the caller (fail-closed — the
// store never holds a partial or invalid write).
func TestSeedEngineConfig_SetManyErrorPropagates(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		SocksProxyEnabled: true,
		SocksProxyPort:    "1080",
		SocksProxyVersion: 5,
	}}
	w := &capturingWriter{err: settings.ErrInvalidSetting}

	if err := enginetopo.SeedEngineConfig(ctx, fc, w); err == nil {
		t.Fatal("SeedEngineConfig: want error when SetMany fails, got nil")
	}
}

// engineTopoDefaults are the settings defaults the testdb-backed seed tests
// resolve against (only the fields the FlareSolverr + SOCKS accessors read
// matter here; every other tunable falls back to its own zero-value default).
func engineTopoDefaults() settings.Defaults {
	return settings.Defaults{
		FlareSolverrEnabled:    false,
		FlareSolverrURL:        "",
		FlareSolverrTimeout:    60,
		FlareSolverrSessionTTL: 15,
		EngineSocksEnabled:     false,
		EngineSocksHost:        "",
		EngineSocksPort:        1080,
		EngineSocksVersion:     5,
	}
}

// TestSeedEngineConfig_StockSuwayomiSeedsFlareSolverrSkipsSocks is the
// regression proof for the reviewer's HIGH defect: against a STOCK Suwayomi
// (SOCKS disabled, socksProxyPort==""), the FlareSolverr values MUST still be
// seeded — the empty SOCKS port must NOT sink the FlareSolverr write — and the
// SOCKS keys are LEFT AT THEIR DEFAULTS (nothing configured to seed), with NO
// error returned. Uses the REAL settings.Service (testdb), not the fake.
func TestSeedEngineConfig_StockSuwayomiSeedsFlareSolverrSkipsSocks(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := settings.NewService(client, engineTopoDefaults())

	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		// FlareSolverr configured (URL set, enabled), the case the owner cares
		// about being migrated.
		FlareSolverrEnabled: true,
		FlareSolverrURL:     "http://flaresolverr.internal:8191",
		FlareSolverrTimeout: 90,
		// SOCKS is a stock Suwayomi's default: disabled, EMPTY port string.
		SocksProxyEnabled: false,
		SocksProxyPort:    "",
	}}

	if err := enginetopo.SeedEngineConfig(ctx, fc, svc); err != nil {
		t.Fatalf("SeedEngineConfig against stock Suwayomi: want no error, got %v", err)
	}

	// FlareSolverr WAS seeded.
	if !svc.FlareSolverrEnabled(ctx) {
		t.Error("FlareSolverrEnabled not seeded (want true)")
	}
	if got := svc.FlareSolverrURL(ctx); got != "http://flaresolverr.internal:8191" {
		t.Errorf("FlareSolverrURL = %q, want the seeded endpoint", got)
	}
	if got := svc.FlareSolverrTimeout(ctx); got != 90 {
		t.Errorf("FlareSolverrTimeout = %d, want 90", got)
	}

	// SOCKS was SKIPPED — the accessors still return the injected defaults, not
	// a seeded (or fake) value.
	if svc.EngineSocksEnabled(ctx) {
		t.Error("EngineSocksEnabled seeded from a disabled/blank engine SOCKS, want default false")
	}
	if got := svc.EngineSocksPort(ctx); got != 1080 {
		t.Errorf("EngineSocksPort = %d, want default 1080 (SOCKS was unconfigured, must not be seeded)", got)
	}
}

// TestSeedEngineConfig_FullyConfiguredSeedsBoth proves that when SOCKS is
// genuinely configured (enabled + a real port) BOTH groups round-trip through
// the real settings.Service.
func TestSeedEngineConfig_FullyConfiguredSeedsBoth(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := settings.NewService(client, engineTopoDefaults())

	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		// FlareSolverr timeout/sessionTtl carry Suwayomi's own NON_NULL defaults
		// (60s / 15m) — both in-range for their tunables.
		FlareSolverrEnabled:    true,
		FlareSolverrURL:        "http://flaresolverr.internal:8191",
		FlareSolverrTimeout:    60,
		FlareSolverrSessionTTL: 15,
		SocksProxyEnabled:      true,
		SocksProxyHost:         "socks.internal",
		SocksProxyPort:         "1081",
		SocksProxyVersion:      4,
	}}

	if err := enginetopo.SeedEngineConfig(ctx, fc, svc); err != nil {
		t.Fatalf("SeedEngineConfig fully configured: %v", err)
	}

	if got := svc.FlareSolverrURL(ctx); got != "http://flaresolverr.internal:8191" {
		t.Errorf("FlareSolverrURL = %q, want the seeded endpoint", got)
	}
	if !svc.EngineSocksEnabled(ctx) {
		t.Error("EngineSocksEnabled not seeded (want true)")
	}
	if got := svc.EngineSocksHost(ctx); got != "socks.internal" {
		t.Errorf("EngineSocksHost = %q, want socks.internal", got)
	}
	if got := svc.EngineSocksPort(ctx); got != 1081 {
		t.Errorf("EngineSocksPort = %d, want 1081", got)
	}
	if got := svc.EngineSocksVersion(ctx); got != 4 {
		t.Errorf("EngineSocksVersion = %d, want 4", got)
	}
}

// TestSeedEngineConfig_SkipsSocksWhenEnabledButBlankPort proves the skip guard
// also covers the inconsistent "enabled but no port" shape — still nothing
// valid to seed, so SOCKS is left at defaults and no error is returned.
func TestSeedEngineConfig_SkipsSocksWhenEnabledButBlankPort(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := settings.NewService(client, engineTopoDefaults())

	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		// FlareSolverr fields at Suwayomi's NON_NULL defaults (in-range).
		FlareSolverrTimeout:    60,
		FlareSolverrSessionTTL: 15,
		SocksProxyEnabled:      true,
		SocksProxyPort:         "",
	}}

	if err := enginetopo.SeedEngineConfig(ctx, fc, svc); err != nil {
		t.Fatalf("SeedEngineConfig enabled-but-blank-port: want no error, got %v", err)
	}
	if svc.EngineSocksEnabled(ctx) {
		t.Error("EngineSocksEnabled seeded despite a blank port, want default false")
	}
}
