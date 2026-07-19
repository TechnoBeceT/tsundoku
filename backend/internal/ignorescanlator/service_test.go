package ignorescanlator_test

import (
	"context"
	"sort"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ignorescanlator"
)

// wantFlagged asserts the store's flagged set equals exactly the given ids.
func wantFlagged(t *testing.T, svc *ignorescanlator.Service, ids ...int64) {
	t.Helper()
	set, err := svc.IgnoreScanlatorSet(context.Background())
	if err != nil {
		t.Fatalf("IgnoreScanlatorSet: %v", err)
	}
	got := make([]int64, 0, len(set))
	for id := range set {
		got = append(got, id)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(got) != len(ids) {
		t.Fatalf("flagged set = %v, want %v", got, ids)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("flagged set = %v, want %v", got, ids)
		}
	}
}

// TestFlag_AddsAndIsIdempotent proves flagging a source records it, and a second
// flag of the same source is absorbed (the unique constraint) — the end-state
// stays exactly {7}.
func TestFlag_AddsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := ignorescanlator.NewService(testdb.New(t))

	wantFlagged(t, svc) // fresh store: nothing flagged

	if err := svc.SetIgnore(ctx, 7, true); err != nil {
		t.Fatalf("SetIgnore(7, true): %v", err)
	}
	wantFlagged(t, svc, 7)

	if err := svc.SetIgnore(ctx, 7, true); err != nil {
		t.Fatalf("SetIgnore(7, true) idempotent: %v", err)
	}
	wantFlagged(t, svc, 7)
}

// TestFlag_IndependentSources proves two flagged sources coexist and un-flagging
// one leaves the other untouched.
func TestFlag_IndependentSources(t *testing.T) {
	ctx := context.Background()
	svc := ignorescanlator.NewService(testdb.New(t))

	if err := svc.SetIgnore(ctx, 7, true); err != nil {
		t.Fatalf("flag 7: %v", err)
	}
	if err := svc.SetIgnore(ctx, 42, true); err != nil {
		t.Fatalf("flag 42: %v", err)
	}
	wantFlagged(t, svc, 7, 42)

	if err := svc.SetIgnore(ctx, 7, false); err != nil {
		t.Fatalf("unflag 7: %v", err)
	}
	wantFlagged(t, svc, 42)
}

// TestUnflag_IsIdempotent proves un-flagging a never-flagged source deletes zero
// rows and is not an error.
func TestUnflag_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc := ignorescanlator.NewService(testdb.New(t))

	if err := svc.SetIgnore(ctx, 7, false); err != nil {
		t.Fatalf("unflag never-flagged source: %v", err)
	}
	wantFlagged(t, svc)
}
