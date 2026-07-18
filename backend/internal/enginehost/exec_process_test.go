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

	got := enginehost.BuildHostEnv(base, 41007, "/config/engine/profiles/abc123")

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
	if got[len(got)-2] != wantPort || got[len(got)-1] != wantData {
		t.Errorf("overrides not appended last: tail = %v", got[len(got)-2:])
	}
	if idxOld := slices.Index(got, "TSUNDOKU_ENGINE_PORT=7777"); idxOld >= slices.Index(got, wantPort) {
		t.Error("override does not come after the inherited default port")
	}
}
