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

// capturingWriter is a settings.SettingsStore test double that records the
// KeyValue batches it was asked to persist (ACCUMULATED across calls, because
// SeedEngineConfig writes FlareSolverr and SOCKS as two separate batches),
// instead of touching a real DB — the mapping + gap-fill logic (ServerSettings →
// keys, skipping already-owned keys) is what these tests exercise; the
// real-Service round-trip is covered by the testdb-backed tests below.
//
// `owned` models the keys Tsundoku already has an explicit Settings row for: it
// seeds ExistingKeys, and every key written via SetMany is added to it, so a
// re-seed inside one test sees the previously-written keys as owned (mirroring
// the real store's gap-fill semantics).
type capturingWriter struct {
	got   []settings.KeyValue
	calls int
	err   error
	errOn int             // when >0, fail SetMany only on the errOn-th call (1-based)
	owned map[string]bool // keys Tsundoku already owns (have an explicit row)
}

func (w *capturingWriter) ExistingKeys(_ context.Context, keys []string) (map[string]bool, error) {
	present := make(map[string]bool, len(keys))
	for _, k := range keys {
		if w.owned[k] {
			present[k] = true
		}
	}
	return present, nil
}

func (w *capturingWriter) SetMany(_ context.Context, updates []settings.KeyValue) error {
	w.calls++
	if w.err != nil && (w.errOn == 0 || w.errOn == w.calls) {
		return w.err
	}
	if w.owned == nil {
		w.owned = make(map[string]bool)
	}
	for _, kv := range updates {
		w.owned[kv.Key] = true
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

// TestSeedEngineConfig_AllKeysOwnedWritesNothing is the gap-fill proof for the
// FULLY-owned case: when every candidate key already has a Settings row (Tsundoku
// owns them all), a re-seed writes NOTHING — SetMany is never called — so a
// restart can never revert an owner's config to the engine's (possibly stale)
// values.
func TestSeedEngineConfig_AllKeysOwnedWritesNothing(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		FlareSolverrEnabled: true,
		FlareSolverrURL:     "http://engine.internal:8191",
		FlareSolverrTimeout: 90,
		SocksProxyEnabled:   true,
		SocksProxyPort:      "1080",
		SocksProxyVersion:   5,
	}}
	// Every candidate key already owned by Tsundoku.
	w := &capturingWriter{owned: map[string]bool{
		settings.KeyFlareSolverrEnabled:          true,
		settings.KeyFlareSolverrURL:              true,
		settings.KeyFlareSolverrTimeout:          true,
		settings.KeyFlareSolverrSessionName:      true,
		settings.KeyFlareSolverrSessionTTL:       true,
		settings.KeyFlareSolverrResponseFallback: true,
		settings.KeyEngineSocksEnabled:           true,
		settings.KeyEngineSocksHost:              true,
		settings.KeyEngineSocksPort:              true,
		settings.KeyEngineSocksVersion:           true,
	}}

	if err := enginetopo.SeedEngineConfig(ctx, fc, w); err != nil {
		t.Fatalf("SeedEngineConfig: %v", err)
	}
	if w.calls != 0 {
		t.Errorf("SetMany called %d times, want 0 (every key already owned → no write)", w.calls)
	}
	if len(w.got) != 0 {
		t.Errorf("wrote %d keys, want 0 (gap-fill must skip owned keys)", len(w.got))
	}
}

// TestSeedEngineConfig_PartialWritesOnlyGaps is the gap-fill proof for the MIXED
// case: keys Tsundoku already owns are left untouched, while the still-unset keys
// are filled from the engine values in the same pass.
func TestSeedEngineConfig_PartialWritesOnlyGaps(t *testing.T) {
	ctx := context.Background()
	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		FlareSolverrEnabled:     true,
		FlareSolverrURL:         "http://engine.internal:8191",
		FlareSolverrTimeout:     90,
		FlareSolverrSessionName: "engine",
		FlareSolverrSessionTTL:  30,
		// SOCKS off — its gap batch is skipped entirely (unchanged behaviour).
		SocksProxyEnabled: false,
		SocksProxyPort:    "",
	}}
	// Owner already owns the URL + the enabled toggle; the rest are gaps.
	w := &capturingWriter{owned: map[string]bool{
		settings.KeyFlareSolverrURL:     true,
		settings.KeyFlareSolverrEnabled: true,
	}}

	if err := enginetopo.SeedEngineConfig(ctx, fc, w); err != nil {
		t.Fatalf("SeedEngineConfig: %v", err)
	}

	// Owned keys were NOT written (never reverted to the engine value).
	if _, ok := w.value(settings.KeyFlareSolverrURL); ok {
		t.Error("owned KeyFlareSolverrURL was written, want skipped")
	}
	if _, ok := w.value(settings.KeyFlareSolverrEnabled); ok {
		t.Error("owned KeyFlareSolverrEnabled was written, want skipped")
	}
	// Gap keys were filled from the engine values.
	if got, ok := w.value(settings.KeyFlareSolverrTimeout); !ok || got != "90" {
		t.Errorf("KeyFlareSolverrTimeout = %q (present=%v), want \"90\" filled as a gap", got, ok)
	}
	if got, ok := w.value(settings.KeyFlareSolverrSessionName); !ok || got != "engine" {
		t.Errorf("KeyFlareSolverrSessionName = %q (present=%v), want \"engine\" filled as a gap", got, ok)
	}
}

// TestSeedEngineConfig_ReSeedDoesNotOverwriteOwnedKey is the end-to-end gap-fill
// proof against the REAL settings.Service (testdb): an owner edit to a
// flaresolverr.* key survives a subsequent seed carrying a different engine
// value — the exact silent-revert the fix prevents. (The still-unset sibling
// keys ARE gap-filled in the same pass, which is correct per-key gap-fill.)
func TestSeedEngineConfig_ReSeedDoesNotOverwriteOwnedKey(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := settings.NewService(client, engineTopoDefaults())

	// The owner has set FlareSolverr URL to their own endpoint.
	if err := svc.Set(ctx, settings.KeyFlareSolverrURL, "http://owner.example:8191"); err != nil {
		t.Fatalf("owner Set: %v", err)
	}

	// A seed then runs carrying a DIFFERENT (stale) engine URL.
	fc := &fakeClient{serverSettings: suwayomi.SuwayomiSettings{
		FlareSolverrEnabled:    true,
		FlareSolverrURL:        "http://stale-engine.internal:8191",
		FlareSolverrTimeout:    60,
		FlareSolverrSessionTTL: 15,
	}}
	if err := enginetopo.SeedEngineConfig(ctx, fc, svc); err != nil {
		t.Fatalf("SeedEngineConfig: %v", err)
	}

	// The owned key is preserved — never reverted to the engine value.
	if got := svc.FlareSolverrURL(ctx); got != "http://owner.example:8191" {
		t.Errorf("FlareSolverrURL = %q, want the owner's value preserved (gap-fill must not overwrite)", got)
	}
	// The unset sibling WAS gap-filled from the engine, proving per-key (not
	// all-or-nothing) gap-fill.
	if !svc.FlareSolverrEnabled(ctx) {
		t.Error("FlareSolverrEnabled not gap-filled (was unset, engine reported true)")
	}
}
