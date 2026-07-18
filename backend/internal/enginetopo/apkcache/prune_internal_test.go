package apkcache

import (
	"reflect"
	"testing"
)

// TestSelectRetained_KeepsNewestN proves the pure partition keeps exactly the
// newest keepNewest versions (newest-first) and marks the rest for removal.
func TestSelectRetained_KeepsNewestN(t *testing.T) {
	retain, remove := selectRetained([]int{1, 5, 3, 9, 7}, 3, nil)
	if want := []int{9, 7, 5}; !reflect.DeepEqual(retain, want) {
		t.Errorf("retain = %v, want %v", retain, want)
	}
	if want := []int{3, 1}; !reflect.DeepEqual(remove, want) {
		t.Errorf("remove = %v, want %v", remove, want)
	}
}

// TestSelectRetained_AlwaysKeepsInstalledEvenIfOld proves the keepAlso set (the
// installed version) survives even when it falls OUTSIDE the newest N — the
// reversible-update invariant that a prune never evicts the running build.
func TestSelectRetained_AlwaysKeepsInstalledEvenIfOld(t *testing.T) {
	// Newest 2 of {1,2,3,4} = {4,3}; installed=1 is older than both but pinned.
	retain, remove := selectRetained([]int{1, 2, 3, 4}, 2, map[int]bool{1: true})
	if want := []int{4, 3, 1}; !reflect.DeepEqual(retain, want) {
		t.Errorf("retain = %v, want %v (installed 1 must survive)", retain, want)
	}
	if want := []int{2}; !reflect.DeepEqual(remove, want) {
		t.Errorf("remove = %v, want %v", remove, want)
	}
}

// TestSelectRetained_NoOpWhenWithinN proves nothing is removed when there are
// no more than keepNewest versions.
func TestSelectRetained_NoOpWhenWithinN(t *testing.T) {
	retain, remove := selectRetained([]int{2, 5}, 3, nil)
	if want := []int{5, 2}; !reflect.DeepEqual(retain, want) {
		t.Errorf("retain = %v, want %v", retain, want)
	}
	if len(remove) != 0 {
		t.Errorf("remove = %v, want empty", remove)
	}
}
