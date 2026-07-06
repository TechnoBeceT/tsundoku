package library

import "testing"

func TestBelowExistingImportances(t *testing.T) {
	cases := []struct {
		name     string
		existing []int
		count    int
		want     []int
	}{
		{"below single existing disk=1", []int{1}, 2, []int{-9, -19}},
		{"below multiple existing", []int{30, 20, 10}, 2, []int{0, -10}},
		{"no existing falls back to adopt scale", nil, 3, []int{30, 20, 10}},
		{"zero count", []int{5}, 0, []int{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := belowExistingImportances(c.existing, c.count)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}
