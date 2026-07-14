package sync_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker/sync"
)

// TestShouldAutoComplete pins the auto-complete rule, including its
// subtlest edge: total==0 (unknown/ongoing) must NEVER auto-complete no
// matter how high lastRead is. This is non-vacuous against a "lastRead>0
// alone completes" bug (the total-0 cases below would wrongly return true
// under that mutation).
func TestShouldAutoComplete(t *testing.T) {
	cases := []struct {
		name     string
		lastRead float64
		total    float64
		want     bool
	}{
		{"total zero, lastRead zero: never completes (ongoing/unknown)", 0, 0, false},
		{"total zero, lastRead positive: never completes (ongoing/unknown total)", 50, 0, false},
		{"total positive, last equal to total: completes", 100, 100, true},
		{"total positive, last less than total: does not complete", 50, 100, false},
		{"total positive, last greater than total: completes", 101, 100, true},
		{"fractional last equal to whole total: completes", 100, 100, true},
		{"fractional last just short of total: does not complete", 99.5, 100, false},
		{"total one, last one: completes", 1, 1, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := sync.ShouldAutoComplete(tt.lastRead, tt.total)
			if got != tt.want {
				t.Fatalf("ShouldAutoComplete(%v, %v) = %v, want %v", tt.lastRead, tt.total, got, tt.want)
			}
		})
	}
}

// TestShouldAutoComplete_TotalZeroNeverCompletes is the dedicated
// adversarial proof of the subtlest rule: sweep a wide range of lastRead
// values against total==0 and assert every single one is false. Non-
// vacuous against any mutation that drops the total>0 guard (e.g.
// simplifying to just "lastRead >= total").
func TestShouldAutoComplete_TotalZeroNeverCompletes(t *testing.T) {
	for _, lastRead := range []float64{0, 1, 0.5, 12.5, 100, 1000, 999999} {
		if sync.ShouldAutoComplete(lastRead, 0) {
			t.Fatalf("ShouldAutoComplete(%v, 0) = true, want false — total=0 (unknown/ongoing) must never auto-complete", lastRead)
		}
	}
}
