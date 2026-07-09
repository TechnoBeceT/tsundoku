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
		// exact boundary: lowest new slot lands at 0 (>= 0 ⇒ still fits, no renumber).
		{"boundary lands at zero", []int{20}, 2, nil, []int{10, 0}},
		// no existing at all: Adopt-scale fallback.
		{"no existing adopt scale", nil, 3, nil, []int{30, 20, 10}},
		// zero count is a no-op.
		{"zero count", []int{5}, 0, nil, []int{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotExisting, gotNew := belowExistingImportances(c.existing, c.count)
			assertIntSlice(t, "existing", gotExisting, c.wantExisting)
			assertIntSlice(t, "new", gotNew, c.wantNew)
		})
	}
}

// TestBelowExistingImportances_NeverNegative sweeps the small-importance inputs
// that used to yield negatives and proves EVERY returned value (new + any
// renumbered existing) is >= 0, strictly descending, and the new batch always
// ranks below the existing providers.
func TestBelowExistingImportances_NeverNegative(t *testing.T) {
	for _, existing := range [][]int{{1}, {0}, {}, {1, 1, 1}} {
		for count := 1; count <= 5; count++ {
			gotExisting, gotNew := belowExistingImportances(existing, count)
			assertNonNegativePlan(t, existing, count, gotExisting, gotNew)
		}
	}
}

// assertNonNegativePlan verifies one belowExistingImportances outcome: the new
// batch is non-negative + strictly descending, and any renumbered existing set
// is non-negative + strictly descending + ranks entirely above the new batch.
func assertNonNegativePlan(t *testing.T, existing []int, count int, gotExisting, gotNew []int) {
	t.Helper()
	assertAllNonNegative(t, "new", gotNew)
	assertStrictlyDescending(t, "new", gotNew)

	if gotExisting == nil {
		return
	}
	if len(gotExisting) != len(existing) {
		t.Fatalf("existing=%v count=%d: renumbered existing len %d, want %d", existing, count, len(gotExisting), len(existing))
	}
	assertAllNonNegative(t, "existing", gotExisting)
	assertStrictlyDescending(t, "existing", gotExisting)
	if len(gotNew) > 0 && gotNew[0] >= gotExisting[len(gotExisting)-1] {
		t.Fatalf("existing=%v count=%d: highest new %d not below lowest existing %d", existing, count, gotNew[0], gotExisting[len(gotExisting)-1])
	}
}

func assertAllNonNegative(t *testing.T, label string, xs []int) {
	t.Helper()
	for _, v := range xs {
		if v < 0 {
			t.Fatalf("%s: negative value in %v", label, xs)
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
