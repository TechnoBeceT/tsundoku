package series

import (
	"testing"
	"time"
)

// TestStalledSeries_Eligibility proves the owner-refined stalled predicate
// (QCAT-297): a series is stalled ONLY when its newest release is older than the
// threshold AND it is still monitored AND not completed. Turning monitoring off
// or marking it completed makes it never stalled (nothing to wait for), and a
// series with no dated chapter at all (latest == nil) is never stalled.
func TestStalledSeries_Eligibility(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -40)  // 40 days ago — past a 30-day threshold
	fresh := now.AddDate(0, 0, -5) // 5 days ago — within the window
	const threshold = 30

	cases := []struct {
		name      string
		latest    *time.Time
		monitored bool
		completed bool
		want      bool
	}{
		{"old + monitored + not completed → stalled", &old, true, false, true},
		{"old but NOT monitored → not stalled", &old, false, false, false},
		{"old but completed → not stalled", &old, true, true, false},
		{"fresh (within threshold) → not stalled", &fresh, true, false, false},
		{"no dated chapter at all → not stalled", nil, true, false, false},
		{"exactly at threshold (30d) → not stalled (strict >)", ptr(now.AddDate(0, 0, -30)), true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stalledSeries(tc.latest, tc.monitored, tc.completed, now, threshold); got != tc.want {
				t.Errorf("stalledSeries = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCoalesceTime proves the upload-date-wins fold behind latestChapterAt: the
// primary (upload) value is used whenever present, otherwise the download-date
// fallback, otherwise nil.
func TestCoalesceTime(t *testing.T) {
	up := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	dl := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if got := coalesceTime(&up, &dl); got == nil || !got.Equal(up) {
		t.Errorf("coalesceTime(up, dl) = %v, want the upload date %v (upload wins even when download is later)", got, up)
	}
	if got := coalesceTime(nil, &dl); got == nil || !got.Equal(dl) {
		t.Errorf("coalesceTime(nil, dl) = %v, want the download fallback %v", got, dl)
	}
	if got := coalesceTime(nil, nil); got != nil {
		t.Errorf("coalesceTime(nil, nil) = %v, want nil", got)
	}
}

func ptr(t time.Time) *time.Time { return &t }
