package suwayomi_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// sourcesStub is a minimal suwayomi.Client that only implements Sources; every
// other method is inherited from the embedded (nil) interface and never called
// by SourceLister. It lets the adapter be unit-tested without a live engine.
type sourcesStub struct {
	suwayomi.Client
	sources []suwayomi.Source
	err     error
}

func (s sourcesStub) Sources(context.Context) ([]suwayomi.Source, error) {
	return s.sources, s.err
}

// TestSourceLister_LoadedSourceIDs proves the adapter parses each source id into
// the set with ok=true, and skips (never fails on) an id that does not parse as
// int64 — a bad id can't match any stored provider anyway.
func TestSourceLister_LoadedSourceIDs(t *testing.T) {
	stub := sourcesStub{sources: []suwayomi.Source{
		{ID: "777", Name: "Comix"},
		{ID: "42", Name: "Asura"},
		{ID: "not-an-int", Name: "Broken"},
	}}

	set, ok, err := suwayomi.NewSourceLister(stub).LoadedSourceIDs(context.Background())
	if err != nil {
		t.Fatalf("LoadedSourceIDs error = %v, want nil", err)
	}
	if !ok {
		t.Fatal("ok = false, want true on a successful Sources call")
	}
	if len(set) != 2 {
		t.Fatalf("set size = %d, want 2 (the unparseable id skipped)", len(set))
	}
	for _, want := range []int64{777, 42} {
		if _, present := set[want]; !present {
			t.Errorf("set missing id %d", want)
		}
	}
}

// TestSourceLister_LoadedSourceIDs_Error proves a Sources failure surfaces
// ok=false + the error so the caller fails safe (flags nothing) rather than
// treating every source as missing.
func TestSourceLister_LoadedSourceIDs_Error(t *testing.T) {
	boom := errors.New("engine unreachable")
	stub := sourcesStub{err: boom}

	set, ok, err := suwayomi.NewSourceLister(stub).LoadedSourceIDs(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want %v", err, boom)
	}
	if ok {
		t.Error("ok = true, want false on a Sources failure")
	}
	if set != nil {
		t.Errorf("set = %v, want nil on failure", set)
	}
}
