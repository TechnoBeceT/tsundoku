package series

import (
	"testing"

	"github.com/google/uuid"
)

// TestNormalizeRanks covers the pure normalization: ordering by submitted
// importance (descending), stable tie-breaking by original slice position, and a
// clean non-negative descending spread regardless of the input values.
func TestNormalizeRanks(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	t.Run("orders by submitted importance descending", func(t *testing.T) {
		// desc order: b(100), c(3), a(-5) → 30, 20, 10.
		got := normalizeRanks([]ProviderRank{
			{SeriesProviderID: a, Importance: -5},
			{SeriesProviderID: b, Importance: 100},
			{SeriesProviderID: c, Importance: 3},
		})
		assertRanks(t, got, []uuid.UUID{b, c, a}, []int{30, 20, 10})
	})

	t.Run("ties keep original slice position", func(t *testing.T) {
		got := normalizeRanks([]ProviderRank{
			{SeriesProviderID: a, Importance: 7},
			{SeriesProviderID: b, Importance: 7},
		})
		assertRanks(t, got, []uuid.UUID{a, b}, []int{20, 10})
	})

	t.Run("all values non-negative even for all-negative input", func(t *testing.T) {
		// desc order: a(-1), b(-9), c(-19) → 30, 20, 10.
		got := normalizeRanks([]ProviderRank{
			{SeriesProviderID: a, Importance: -1},
			{SeriesProviderID: b, Importance: -9},
			{SeriesProviderID: c, Importance: -19},
		})
		assertRanks(t, got, []uuid.UUID{a, b, c}, []int{30, 20, 10})
	})

	t.Run("empty input yields empty output", func(t *testing.T) {
		if got := normalizeRanks(nil); len(got) != 0 {
			t.Fatalf("normalizeRanks(nil) = %v, want empty", got)
		}
	})
}

// assertRanks fails unless got matches the expected id order and importance spread.
func assertRanks(t *testing.T, got []ProviderRank, wantOrder []uuid.UUID, wantImp []int) {
	t.Helper()
	if len(got) != len(wantOrder) {
		t.Fatalf("len = %d, want %d (%v)", len(got), len(wantOrder), got)
	}
	for i, r := range got {
		if r.SeriesProviderID != wantOrder[i] {
			t.Fatalf("slot %d id = %v, want %v", i, r.SeriesProviderID, wantOrder[i])
		}
		if r.Importance != wantImp[i] {
			t.Fatalf("slot %d importance = %d, want %d", i, r.Importance, wantImp[i])
		}
	}
}
