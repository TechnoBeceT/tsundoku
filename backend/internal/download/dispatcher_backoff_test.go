// White-box unit tests for defaultBackoff. No DB or Docker required.
package download

import (
	"testing"
	"time"
)

// TestDefaultBackoff verifies that defaultBackoff never returns a negative
// value, correctly starts at 5 minutes for attempt 0, and caps at 1 hour for
// all large attempt values.
func TestDefaultBackoff(t *testing.T) {
	t.Run("attempt_0_is_5_minutes", func(t *testing.T) {
		got := defaultBackoff(0)
		if got != 5*time.Minute {
			t.Errorf("attempt 0: want 5m, got %v", got)
		}
	})

	t.Run("mid_attempt_is_hour_capped", func(t *testing.T) {
		// shift=4 → 5min×16=80min > 1h, so the cap fires at attempt≥4.
		for _, attempt := range []int{4, 5, 6, 10} {
			got := defaultBackoff(attempt)
			if got != time.Hour {
				t.Errorf("attempt %d: want 1h (capped), got %v", attempt, got)
			}
		}
	})

	t.Run("large_attempt_positive_and_capped", func(t *testing.T) {
		// Attempts well above the shift cap must still be positive and ≤ 1h.
		for _, attempt := range []int{13, 20, 30, 60, 100, 1000} {
			got := defaultBackoff(attempt)
			if got <= 0 {
				t.Errorf("attempt %d: got non-positive backoff %v (overflow?)", attempt, got)
			}
			if got > time.Hour {
				t.Errorf("attempt %d: got %v > 1h (cap not applied)", attempt, got)
			}
		}
	})
}
