package sync_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker/sync"
)

// TestTruncateForInteger pins floor (never round) semantics: an integer-
// count tracker must receive a whole chapter that was ACTUALLY read, never
// a rounded-up chapter that wasn't. A round-to-nearest mutation would fail
// the 12.5/12.9 cases below.
func TestTruncateForInteger(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want int
	}{
		{"whole number stays the same", 12, 12},
		{"fractional floors down, does not round", 12.5, 12},
		{"fractional close to next whole still floors down", 12.9, 12},
		{"fractional just above whole floors down", 12.1, 12},
		{"zero stays zero", 0, 0},
		{"small fraction floors to zero", 0.5, 0},
		{"large whole number stays the same", 999, 999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sync.TruncateForInteger(tt.in)
			if got != tt.want {
				t.Fatalf("TruncateForInteger(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
