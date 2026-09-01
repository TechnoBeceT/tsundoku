package enginehost_test

import (
	"slices"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

// TestBuildHostEnv_AppendsPortAndDataDir proves the per-instance env carries the
// port + data-dir overrides on top of the inherited base, and that they are
// appended LAST so they win over any inherited TSUNDOKU_ENGINE_* value (a
// launched profile must never share the default instance's 7777 / data dir).
func TestBuildHostEnv_AppendsPortAndDataDir(t *testing.T) {
	base := []string{
		"DISPLAY=:99",
		"TSUNDOKU_ENGINE_KCEF=true",
		"TSUNDOKU_ENGINE_PORT=7777", // the default instance's value — must be overridden
		"TSUNDOKU_ENGINE_DATA=/config/engine",
	}

	got := enginehost.BuildHostEnv(base, 41007, "/config/engine/profiles/abc123", false)

	// The inherited entries are preserved.
	for _, want := range []string{"DISPLAY=:99", "TSUNDOKU_ENGINE_KCEF=true"} {
		if !slices.Contains(got, want) {
			t.Errorf("env missing inherited %q; got %v", want, got)
		}
	}
	// The overrides are present…
	wantPort := "TSUNDOKU_ENGINE_PORT=41007"
	wantData := "TSUNDOKU_ENGINE_DATA=/config/engine/profiles/abc123"
	if !slices.Contains(got, wantPort) || !slices.Contains(got, wantData) {
		t.Fatalf("env missing overrides %q / %q; got %v", wantPort, wantData, got)
	}
	// …and appended LAST (exec's env is last-wins), after the inherited 7777.
	if got[len(got)-3] != wantPort || got[len(got)-2] != wantData || got[len(got)-1] != "TSUNDOKU_ENGINE_KCEF=false" {
		t.Errorf("overrides not appended last: tail = %v", got[len(got)-3:])
	}
	if idxOld := slices.Index(got, "TSUNDOKU_ENGINE_PORT=7777"); idxOld >= slices.Index(got, wantPort) {
		t.Error("override does not come after the inherited default port")
	}
	// Every profile receives an explicit KCEF setting, even when it matches the
	// inherited default.
	if got[len(got)-1] != "TSUNDOKU_ENGINE_KCEF=false" {
		t.Errorf("env tail = %q, want explicit KCEF=false", got[len(got)-1])
	}
}

// TestBuildHostEnv_DisabledKCEFOverridesInheritedTrue proves that KCEF=false
// appends TSUNDOKU_ENGINE_KCEF=false LAST, so it wins over the inherited
// TSUNDOKU_ENGINE_KCEF=true (the default instance's value) — a FlareSolverr-backed
// profile spawns with no Chromium (GAP-094). The engine-host enables KCEF only
// when the value equals "true", so "false" reliably disables it.
func TestBuildHostEnv_DisabledKCEFOverridesInheritedTrue(t *testing.T) {
	base := []string{
		"TSUNDOKU_ENGINE_KCEF=true", // the default instance's value — must be overridden
	}

	got := enginehost.BuildHostEnv(base, 41007, "/config/engine/profiles/abc123", false)

	if !slices.Contains(got, "TSUNDOKU_ENGINE_KCEF=false") {
		t.Fatalf("KCEF=false must append TSUNDOKU_ENGINE_KCEF=false; got %v", got)
	}
	// The override must come AFTER the inherited true (exec's env is last-wins).
	idxTrue := slices.Index(got, "TSUNDOKU_ENGINE_KCEF=true")
	idxFalse := slices.Index(got, "TSUNDOKU_ENGINE_KCEF=false")
	if idxTrue >= 0 && idxFalse <= idxTrue {
		t.Errorf("KCEF=false (idx %d) must come after inherited KCEF=true (idx %d)", idxFalse, idxTrue)
	}
}

// TestBuildHostEnv_EnabledKCEFOverridesInheritedFalse keeps every spawned
// profile explicit: a required WebView must not inherit a stale false value from
// the parent process.
func TestBuildHostEnv_EnabledKCEFOverridesInheritedFalse(t *testing.T) {
	got := enginehost.BuildHostEnv(
		[]string{"TSUNDOKU_ENGINE_KCEF=false"},
		41007,
		"/config/engine/profiles/abc123",
		true,
	)
	if got[len(got)-1] != "TSUNDOKU_ENGINE_KCEF=true" {
		t.Fatalf("env tail = %q, want explicit KCEF=true", got[len(got)-1])
	}
}
