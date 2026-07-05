// White-box unit tests for roundRobinBySeries — the pure per-source reordering
// helper that interleaves a source's queue across series (schedule.go). No DB
// or Docker required: resolvedChapter is exercised directly with synthetic ids.
package download

import (
	"testing"

	"github.com/google/uuid"
)

// mkResolved builds a minimal resolvedChapter for round-robin ordering tests —
// only seriesID matters for roundRobinBySeries, so chapterID is a fresh random
// id purely to make failures easy to distinguish in test output.
func mkResolved(seriesID uuid.UUID) resolvedChapter {
	return resolvedChapter{chapterID: uuid.New(), seriesID: seriesID}
}

// seriesIDs extracts the seriesID of each item, for compact comparison against
// an expected rotation order.
func seriesIDs(items []resolvedChapter) []uuid.UUID {
	out := make([]uuid.UUID, len(items))
	for i, it := range items {
		out[i] = it.seriesID
	}
	return out
}

// TestRoundRobinBySeries_InterleavesUnevenSeries proves the core interleaving
// rule: three series of DIFFERENT lengths, all already in ascending
// (first-appearance) order as WantedChapters would deliver them, are emitted
// round-robin — series[0]'s item, series[1]'s item, series[2]'s item, then back
// to series[0], draining each series in its own internal order and skipping any
// series once it runs out.
func TestRoundRobinBySeries_InterleavesUnevenSeries(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	// Input as groupBySource would hand it in (number-ascending overall): A has
	// 3 chapters, B has 1, C has 2, and A appears first overall.
	input := []resolvedChapter{
		mkResolved(a), mkResolved(a), mkResolved(b), mkResolved(a), mkResolved(c), mkResolved(c),
	}

	got := seriesIDs(roundRobinBySeries(input))
	want := []uuid.UUID{a, b, c, a, c, a}

	if len(got) != len(want) {
		t.Fatalf("len(got) = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("position %d: series = %v, want %v (full got=%v)", i, got[i], w, got)
		}
	}
}

// TestRoundRobinBySeries_SingleSeriesUnchanged proves a single-series queue is
// returned in its original order (round-robin across one series is a no-op) —
// the common case, since most sources back only one in-progress series.
func TestRoundRobinBySeries_SingleSeriesUnchanged(t *testing.T) {
	only := uuid.New()
	input := []resolvedChapter{mkResolved(only), mkResolved(only), mkResolved(only)}
	wantIDs := []uuid.UUID{input[0].chapterID, input[1].chapterID, input[2].chapterID}

	got := roundRobinBySeries(input)
	if len(got) != len(wantIDs) {
		t.Fatalf("len = %d, want %d", len(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		if got[i].chapterID != id {
			t.Errorf("single-series order must be preserved at position %d", i)
		}
	}
}

// TestRoundRobinBySeries_EmptyAndSingleItem covers the pure function's edge
// inputs: nil/empty stays empty, and a single item is returned unchanged.
func TestRoundRobinBySeries_EmptyAndSingleItem(t *testing.T) {
	if got := roundRobinBySeries(nil); len(got) != 0 {
		t.Errorf("nil input: want empty, got %v", got)
	}
	if got := roundRobinBySeries([]resolvedChapter{}); len(got) != 0 {
		t.Errorf("empty input: want empty, got %v", got)
	}

	only := uuid.New()
	one := []resolvedChapter{mkResolved(only)}
	got := roundRobinBySeries(one)
	if len(got) != 1 || got[0].seriesID != only {
		t.Errorf("single item: want unchanged, got %v", got)
	}
}

// TestRoundRobinBySeries_LateJoiningSeriesNotStarvedBehindBigBacklog reproduces
// the exact scenario from the Slice 2 plan: series X has 10 wanted chapters
// (numbers 1..10), series Y has a SINGLE wanted chapter at a much higher number
// (e.g. a source just added to an already-partway-through series). Because
// WantedChapters orders by number ascending, X's whole backlog sorts before Y's
// one item in the RAW input — exactly the starve-behind-a-big-backlog bug.
// roundRobinBySeries must place Y's item within the first `batch` (4) selections
// rather than at the tail.
func TestRoundRobinBySeries_LateJoiningSeriesNotStarvedBehindBigBacklog(t *testing.T) {
	x, y := uuid.New(), uuid.New()
	var input []resolvedChapter
	for range 10 {
		input = append(input, mkResolved(x))
	}
	input = append(input, mkResolved(y)) // Y's one chapter sorts LAST pre-RR

	got := roundRobinBySeries(input)

	const batch = 4
	found := false
	for _, it := range got[:batch] {
		if it.seriesID == y {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("series Y's late-joining chapter was starved out of the first %d selections (got order=%v)", batch, seriesIDs(got))
	}
}
