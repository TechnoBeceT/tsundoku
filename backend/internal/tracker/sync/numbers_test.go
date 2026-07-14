package sync_test

import (
	"math"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker/sync"
)

// TestSyncableNumbers pins the unparseable-filter rule: the -1 sentinel,
// any other negative, and NaN are dropped; every valid (including
// fractional and zero) chapter number survives, in order. Non-vacuous
// against a "keep everything" bug (the -1/negative/NaN cases below would
// leak through) and against an "also drop valid numbers" bug (the keep
// cases would go missing).
func TestSyncableNumbers(t *testing.T) {
	tests := []struct {
		name string
		in   []float64
		want []float64
	}{
		{
			name: "drops the unparseable -1 sentinel",
			in:   []float64{1, -1, 2},
			want: []float64{1, 2},
		},
		{
			name: "drops other negative values",
			in:   []float64{1, -5, -0.5, 2},
			want: []float64{1, 2},
		},
		{
			name: "drops NaN",
			in:   []float64{1, math.NaN(), 2},
			want: []float64{1, 2},
		},
		{
			name: "keeps fractional chapters",
			in:   []float64{1, 1.5, 2, 12.5},
			want: []float64{1, 1.5, 2, 12.5},
		},
		{
			name: "keeps zero (chapter 0 is a valid prologue number)",
			in:   []float64{0, 1, 2},
			want: []float64{0, 1, 2},
		},
		{
			name: "empty input yields empty output",
			in:   []float64{},
			want: []float64{},
		},
		{
			name: "all unparseable yields empty output",
			in:   []float64{-1, -1, math.NaN()},
			want: []float64{},
		},
		{
			name: "preserves input order, does not sort",
			in:   []float64{5, 1, 3},
			want: []float64{5, 1, 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sync.SyncableNumbers(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("SyncableNumbers(%v) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("SyncableNumbers(%v) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

// TestMarkReadUpTo_AscendingRun proves a clean ascending run is counted
// exactly up to (and including) remoteLastRead.
func TestMarkReadUpTo_AscendingRun(t *testing.T) {
	tests := []struct {
		name           string
		numbers        []float64
		remoteLastRead float64
		want           int
	}{
		{"all below remote counts all", []float64{1, 2, 3}, 10, 3},
		{"exact boundary counts through the boundary chapter", []float64{1, 2, 3}, 3, 3},
		{"boundary mid-run stops counting after remoteLastRead", []float64{1, 2, 3}, 2, 2},
		{"remote below every chapter counts none", []float64{1, 2, 3}, 0, 0},
		{"fractional chapters respect the boundary", []float64{1, 1.5, 2}, 1.5, 2},
		{"empty input counts none", []float64{}, 10, 0},
		{"single chapter at or below remote counts one", []float64{1}, 1, 1},
		{"single chapter above remote counts none", []float64{5}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sync.MarkReadUpTo(tt.numbers, tt.remoteLastRead)
			if got != tt.want {
				t.Fatalf("MarkReadUpTo(%v, %v) = %v, want %v", tt.numbers, tt.remoteLastRead, got, tt.want)
			}
		})
	}
}

// TestMarkReadUpTo_MonotonicStop is the adversarial proof of the subtlest
// rule in the kernel: the walk STOPS PERMANENTLY at the first non-
// monotonic (re-descending or duplicate) chapter number, even when later
// numbers in the slice would otherwise qualify (<= remoteLastRead). This is
// non-vacuous against a "count every number <= remoteLastRead regardless of
// order" bug: that bug would return 5 for "1,2,3,1,4" with remoteLastRead=4
// (all five numbers are <= 4); the correct, corruption-surviving answer is
// 3 (stop at the re-descending "1").
func TestMarkReadUpTo_MonotonicStop(t *testing.T) {
	tests := []struct {
		name           string
		numbers        []float64
		remoteLastRead float64
		want           int
	}{
		{
			name:           "the canonical 1,2,3,1,4 corruption stops at the re-descend",
			numbers:        []float64{1, 2, 3, 1, 4},
			remoteLastRead: 4,
			want:           3,
		},
		{
			name:           "re-descend still stops the walk even when remoteLastRead is far beyond it",
			numbers:        []float64{1, 2, 3, 1, 4},
			remoteLastRead: 100,
			want:           3,
		},
		{
			name:           "a duplicated chapter number stops the walk (not strictly increasing)",
			numbers:        []float64{1, 2, 2, 3},
			remoteLastRead: 10,
			want:           2,
		},
		{
			name:           "corruption at the very first pair still yields a count of one",
			numbers:        []float64{5, 5, 6},
			remoteLastRead: 10,
			want:           1,
		},
		{
			name:           "corruption immediately after the first element",
			numbers:        []float64{1, 0.5, 2},
			remoteLastRead: 10,
			want:           1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sync.MarkReadUpTo(tt.numbers, tt.remoteLastRead)
			if got != tt.want {
				t.Fatalf("MarkReadUpTo(%v, %v) = %v, want %v (monotonic-stop must hold)", tt.numbers, tt.remoteLastRead, got, tt.want)
			}
		})
	}
}

// TestMarkReadUpTo_NaiveCountWouldDiffer directly contrasts the correct
// monotonic-stop result against what a naive "count every number <=
// remoteLastRead" implementation would produce, proving the two are
// different outputs on the same input (i.e. the monotonic-stop test above
// cannot be satisfied by the naive, wrong implementation).
func TestMarkReadUpTo_NaiveCountWouldDiffer(t *testing.T) {
	numbers := []float64{1, 2, 3, 1, 4}
	remoteLastRead := 4.0

	naiveCount := 0
	for _, n := range numbers {
		if n <= remoteLastRead {
			naiveCount++
		}
	}
	if naiveCount != 5 {
		t.Fatalf("test setup sanity check failed: naive count = %v, want 5", naiveCount)
	}

	got := sync.MarkReadUpTo(numbers, remoteLastRead)
	if got == naiveCount {
		t.Fatalf("MarkReadUpTo(%v, %v) = %v matches the naive (order-blind) count %v — the monotonic-stop rule is not being enforced", numbers, remoteLastRead, got, naiveCount)
	}
	if got != 3 {
		t.Fatalf("MarkReadUpTo(%v, %v) = %v, want 3", numbers, remoteLastRead, got)
	}
}
