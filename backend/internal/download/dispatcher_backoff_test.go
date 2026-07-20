// White-box unit tests for backoffCurve. No DB or Docker required.
package download

import (
	"testing"
	"time"
)

// TestBackoffCurve verifies the FLAT retry interval: backoffCurve returns the
// configured base delay itself, identically for every attempt (no per-attempt
// growth, no cap), and returns 0 for a 0 base (the instant-retry seam tests rely
// on). This is the Kaizoku-style "count every retry, terminal at max" model —
// drain-prevention is the circuit-breaker, not a growing backoff.
func TestBackoffCurve(t *testing.T) {
	t.Run("returns_base_flat", func(t *testing.T) {
		base := 30 * time.Minute
		if got := backoffCurve(base); got != base {
			t.Errorf("flat interval: want %v, got %v", base, got)
		}
	})

	t.Run("independent_of_call_count", func(t *testing.T) {
		base := 90 * time.Second
		// Called many times, the interval never changes (no attempt-driven growth).
		for i := 0; i < 20; i++ {
			if got := backoffCurve(base); got != base {
				t.Errorf("call %d: want %v (flat), got %v", i, base, got)
			}
		}
	})

	t.Run("no_cap", func(t *testing.T) {
		// A base above the old 1h ceiling is returned verbatim — the cap is gone.
		base := 3 * time.Hour
		if got := backoffCurve(base); got != base {
			t.Errorf("large base: want %v (no cap), got %v", base, got)
		}
	})

	t.Run("zero_base_is_instant", func(t *testing.T) {
		if got := backoffCurve(0); got != 0 {
			t.Errorf("0 base: want 0, got %v", got)
		}
	})
}
