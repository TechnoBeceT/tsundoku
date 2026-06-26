// White-box unit tests for backoffCurve. No DB or Docker required.
package download

import (
	"testing"
	"time"
)

// TestBackoffCurve verifies the runtime-base backoff curve: it starts at the
// base for attempt 0, doubles per attempt, caps at 1 hour, stays positive for
// very large attempts, and returns 0 for a 0 base (the instant-retry seam tests
// rely on).
func TestBackoffCurve(t *testing.T) {
	t.Run("attempt_0_is_base", func(t *testing.T) {
		base := 90 * time.Second
		if got := backoffCurve(base, 0); got != base {
			t.Errorf("attempt 0: want %v, got %v", base, got)
		}
	})

	t.Run("doubles_per_attempt", func(t *testing.T) {
		base := time.Minute
		if got := backoffCurve(base, 2); got != 4*time.Minute {
			t.Errorf("attempt 2: want 4m, got %v", got)
		}
	})

	t.Run("hour_capped", func(t *testing.T) {
		base := time.Minute
		// 1m×2^6 = 64m > 1h, so the cap fires for attempt >= 6.
		for _, attempt := range []int{6, 8, 12, 20} {
			if got := backoffCurve(base, attempt); got != time.Hour {
				t.Errorf("attempt %d: want 1h (capped), got %v", attempt, got)
			}
		}
	})

	t.Run("large_attempt_positive_and_capped", func(t *testing.T) {
		base := time.Hour
		for _, attempt := range []int{13, 20, 100, 1000} {
			got := backoffCurve(base, attempt)
			if got <= 0 {
				t.Errorf("attempt %d: got non-positive backoff %v (overflow?)", attempt, got)
			}
			if got > time.Hour {
				t.Errorf("attempt %d: got %v > 1h (cap not applied)", attempt, got)
			}
		}
	})

	t.Run("zero_base_is_instant", func(t *testing.T) {
		for _, attempt := range []int{0, 1, 5, 100} {
			if got := backoffCurve(0, attempt); got != 0 {
				t.Errorf("attempt %d with 0 base: want 0, got %v", attempt, got)
			}
		}
	})
}
