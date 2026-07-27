package library

import "testing"

func TestBelowExistingImportances(t *testing.T) {
	cases := []struct {
		name         string
		existing     []int
		count        int
		wantExisting []int // nil = existing providers left untouched
		wantNew      []int
	}{
		// disk-origin series (importance 1): no room below, whole set renumbered.
		{"disk=1 renumber", []int{1}, 2, []int{30}, []int{20, 10}},
		{"disk=1 single", []int{1}, 1, []int{20}, []int{10}},
		// existing at 0: still no room below, renumber.
		{"existing=0 renumber", []int{0}, 1, []int{20}, []int{10}},
		// three cramped existing providers all renumbered above the new one.
		{"triple ones renumber", []int{1, 1, 1}, 1, []int{40, 30, 20}, []int{10}},
		// plenty of room below: existing untouched, new packed underneath.
		{"room below min", []int{50, 40, 30}, 2, nil, []int{20, 10}},
		// no existing at all: Adopt-scale fallback.
		{"no existing adopt scale", nil, 3, nil, []int{30, 20, 10}},
		// zero count is a no-op.
		{"zero count", []int{5}, 0, nil, []int{}},
		// The exact-boundary case (count*step == minExisting), which used to pass
		// the room test and plan the LOWEST new slot onto the reserved sentinel 0.
		// It now takes the renumber branch and every slot is a real rank.
		{"boundary no longer lands on the sentinel", []int{20}, 2, []int{30}, []int{20, 10}},
		// The shape seen in production: two ranked sources plus a six-source batch
		// gave 50, 40, 30, 20, 10, 0 — the last source stranded on the sentinel.
		{"six-source batch under 70/60", []int{70, 60}, 6, []int{80, 70}, []int{60, 50, 40, 30, 20, 10}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotExisting, gotNew := belowExistingImportances(c.existing, c.count)
			assertIntSlice(t, "existing", gotExisting, c.wantExisting)
			assertIntSlice(t, "new", gotNew, c.wantNew)
		})
	}
}

// TestBelowExistingImportances_AlwaysPlansRealRanks sweeps the small-importance
// inputs that used to yield NEGATIVE new importances, plus the exact-boundary
// inputs (count*step == minExisting) that used to plan the lowest new slot onto
// the reserved park sentinel 0, and proves the invariant the rest of the system
// depends on: every value this planner emits — new or renumbered — is at least
// importanceStep, so it is always a real rank.
//
// That bound matters beyond tidiness. Importance 0 is the sentinel a library
// merge parks a provider on while it renames a series' CBZ files, and the upgrade
// engine reads it as "no rank at all", so a brand-new source planned onto it can
// never heal a chapter's satisfied-importance watermark and is ranked below every
// sibling — permanently, since nothing will ever un-park a row that was never
// parked. Bounding the whole plan at importanceStep makes that unreachable by
// construction rather than by a special case.
//
// It also keeps the original ordering guarantees: both halves strictly descending
// and the whole new batch below the existing providers.
func TestBelowExistingImportances_AlwaysPlansRealRanks(t *testing.T) {
	for _, existing := range [][]int{{1}, {0}, {}, {1, 1, 1}, {20}, {50, 1}, {70, 60}, {30, 20, 10}} {
		for count := 1; count <= 8; count++ {
			gotExisting, gotNew := belowExistingImportances(existing, count)
			assertRankPlan(t, existing, count, gotExisting, gotNew)
		}
	}
}

// assertRankPlan verifies one belowExistingImportances outcome: both halves are
// real ranks (>= importanceStep) and strictly descending, the renumbered existing
// set stays index-aligned to its input, and the new batch ranks entirely below
// the existing providers.
func assertRankPlan(t *testing.T, existing []int, count int, gotExisting, gotNew []int) {
	t.Helper()
	assertAllRealRanks(t, "new", gotNew)
	assertStrictlyDescending(t, "new", gotNew)

	if gotExisting == nil {
		return
	}
	if len(gotExisting) != len(existing) {
		t.Fatalf("existing=%v count=%d: renumbered existing len %d, want %d", existing, count, len(gotExisting), len(existing))
	}
	assertAllRealRanks(t, "existing", gotExisting)
	assertStrictlyDescending(t, "existing", gotExisting)
	if len(gotNew) > 0 && gotNew[0] >= gotExisting[len(gotExisting)-1] {
		t.Fatalf("existing=%v count=%d: highest new %d not below lowest existing %d", existing, count, gotNew[0], gotExisting[len(gotExisting)-1])
	}
}

// assertAllRealRanks fails unless every value is at least importanceStep — the
// bound that keeps a planned rank away from both negatives and the reserved park
// sentinel 0.
func assertAllRealRanks(t *testing.T, label string, xs []int) {
	t.Helper()
	for _, v := range xs {
		if v < importanceStep {
			t.Fatalf("%s: %v contains %d, want every value >= %d", label, xs, v, importanceStep)
		}
	}
}

func assertIntSlice(t *testing.T, label string, got, want []int) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: len = %d %v, want %d %v", label, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
}

func assertStrictlyDescending(t *testing.T, label string, xs []int) {
	t.Helper()
	for i := 1; i < len(xs); i++ {
		if xs[i] >= xs[i-1] {
			t.Fatalf("%s: not strictly descending: %v", label, xs)
		}
	}
}
