package disabledsource_test

import (
	"context"
	"sort"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disabledsource"
)

// wantDisabled asserts the store's disabled set equals exactly the given ids.
func wantDisabled(t *testing.T, svc *disabledsource.Service, ids ...int64) {
	t.Helper()
	set, err := svc.Disabled(context.Background())
	if err != nil {
		t.Fatalf("Disabled: %v", err)
	}
	got := make([]int64, 0, len(set))
	for id := range set {
		got = append(got, id)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(got) != len(ids) {
		t.Fatalf("disabled set = %v, want %v", got, ids)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("disabled set = %v, want %v", got, ids)
		}
	}
}

// TestDisable_AddsAndIsIdempotent proves disabling a source records it, and a
// second disable of the same source is absorbed (the unique constraint) — the
// end-state stays exactly {7}.
func TestDisable_AddsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := disabledsource.NewService(testdb.New(t))

	wantDisabled(t, svc) // fresh store: nothing disabled

	if err := svc.SetEnabled(ctx, 7, false); err != nil {
		t.Fatalf("SetEnabled(7, false): %v", err)
	}
	wantDisabled(t, svc, 7)

	if err := svc.SetEnabled(ctx, 7, false); err != nil {
		t.Fatalf("SetEnabled(7, false) idempotent: %v", err)
	}
	wantDisabled(t, svc, 7)
}

// TestDisable_IndependentSources proves two disabled sources coexist and
// re-enabling one leaves the other untouched.
func TestDisable_IndependentSources(t *testing.T) {
	ctx := context.Background()
	svc := disabledsource.NewService(testdb.New(t))

	if err := svc.SetEnabled(ctx, 7, false); err != nil {
		t.Fatalf("disable 7: %v", err)
	}
	if err := svc.SetEnabled(ctx, 42, false); err != nil {
		t.Fatalf("disable 42: %v", err)
	}
	wantDisabled(t, svc, 7, 42)

	if err := svc.SetEnabled(ctx, 7, true); err != nil {
		t.Fatalf("enable 7: %v", err)
	}
	wantDisabled(t, svc, 42)
}

// TestEnable_IsIdempotent proves re-enabling an already-enabled source deletes
// zero rows and is not an error.
func TestEnable_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := disabledsource.NewService(testdb.New(t))

	if err := svc.SetEnabled(ctx, 7, true); err != nil {
		t.Fatalf("enable never-disabled source: %v", err)
	}
	wantDisabled(t, svc)
}
