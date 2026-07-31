package series_test

import (
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// TestEarlyAccessUntil pins the early-access predicate (GAP-141): a feed row counts
// as WITHHELD only when its stored per-source last_error classifies as the `locked`
// category AND the deferral the engine wrote is still in force. Every other shape —
// an ordinary failure, a lapsed deferral, no deferral at all — is not early access,
// so the read models keep calling it what it is.
func TestEarlyAccessUntil(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	future := now.Add(72 * time.Hour)
	past := now.Add(-time.Minute)

	tests := []struct {
		name string
		pc   *ent.ProviderChapter
		want *time.Time
	}{
		{
			name: "locked message with an in-force deferral",
			pc:   &ent.ProviderChapter{LastError: "Chapter locked: coins required", NextAttemptAt: &future},
			want: &future,
		},
		{
			name: "locked wording is matched case-insensitively",
			pc:   &ent.ProviderChapter{LastError: "upstream error: PREMIUM CHAPTER", NextAttemptAt: &future},
			want: &future,
		},
		{
			name: "ordinary source failure is not early access",
			pc:   &ent.ProviderChapter{LastError: "connection reset by peer", NextAttemptAt: &future},
			want: nil,
		},
		{
			name: "lapsed deferral is no longer in force",
			pc:   &ent.ProviderChapter{LastError: "chapter locked", NextAttemptAt: &past},
			want: nil,
		},
		{
			name: "locked message with no deferral written",
			pc:   &ent.ProviderChapter{LastError: "chapter locked"},
			want: nil,
		},
		{
			name: "no error at all",
			pc:   &ent.ProviderChapter{NextAttemptAt: &future},
			want: nil,
		},
		{
			name: "nil feed row",
			pc:   nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := series.EarlyAccessUntil(tt.pc, now)
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("EarlyAccessUntil = %v, want nil", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("EarlyAccessUntil = nil, want %v", *tt.want)
			case tt.want != nil && !got.Equal(*tt.want):
				t.Fatalf("EarlyAccessUntil = %v, want %v", *got, *tt.want)
			}
		})
	}
}

// TestEarlyAccessUnlessSettled_SuppressedWhenTheChapterHasAFile pins the rule that
// a chapter ALREADY ON DISK is never "waiting for early access", whatever its
// feed rows say (GAP-141).
//
// The reachable case is a convergence upgrade, not a download: a chapter arrives
// from a free mirror, a higher-importance source is flagged as an upgrade, that
// upgrade fetch comes back "Chapter locked (coins required)", and deferSource
// writes the locked last_error + a 72h deferral onto the better source's feed row
// while the chapter itself goes back to `downloaded`. Keying the badge on the feed
// row alone then hid the state badge of a file the owner can read RIGHT NOW.
//
// This is the QCAT-343 lesson repeating one layer up: readability is a property of
// the FILE, never of the surrounding state, and every consumer shares one predicate.
func TestEarlyAccessUnlessSettled_SuppressedWhenTheChapterHasAFile(t *testing.T) {
	until := time.Now().UTC().Add(72 * time.Hour)

	if got := series.EarlyAccessUnlessSettled(entchapter.StateDownloaded, "[Comix][en] Coin Gate 2 007.cbz", &until); got != nil {
		t.Fatalf("EarlyAccessUnlessSettled(with file) = %v, want nil (a readable chapter is not withheld)", got)
	}
	if got := series.EarlyAccessUnlessSettled(entchapter.StateFailed, "", &until); got == nil || !got.Equal(until) {
		t.Fatalf("EarlyAccessUnlessSettled(no file) = %v, want %v (a fileless chapter still reports its wait)", got, until)
	}
	if got := series.EarlyAccessUnlessSettled(entchapter.StateFailed, "", nil); got != nil {
		t.Fatalf("EarlyAccessUnlessSettled(not withheld) = %v, want nil", got)
	}
}

// TestEarlyAccessUnlessSettled_SuppressedWhenTheChapterIsParked pins the other half
// of the rule (GAP-141): a chapter the library has DELIBERATELY stopped fetching is
// not waiting for anything, so the marker — which promises "free in ~3d" and, in the
// UI, REPLACES the state badge — must not claim otherwise.
//
// Neither parked state can be caught by the file test, because both are states in
// which the chapter deliberately has no file: download.supersedeOnePart DELETES a
// superseded part's CBZ and clears Chapter.filename, and an ignored fractional was
// never downloaded at all. Both are reachable while a withheld feed row survives —
// a part is superseded once its whole lands, and series.applyIgnoreReconcile parks
// a `failed` fractional (the very state a withheld chapter rests in) as ignored.
//
// The states that DO wait keep their wait, so the deny-list stays narrow.
func TestEarlyAccessUnlessSettled_SuppressedWhenTheChapterIsParked(t *testing.T) {
	until := time.Now().UTC().Add(72 * time.Hour)

	for _, state := range []entchapter.State{entchapter.StateSuperseded, entchapter.StateIgnored} {
		if got := series.EarlyAccessUnlessSettled(state, "", &until); got != nil {
			t.Errorf("EarlyAccessUnlessSettled(%s) = %v, want nil (nothing will fetch a parked chapter)", state, got)
		}
	}
	for _, state := range []entchapter.State{
		entchapter.StateWanted, entchapter.StateDownloading,
		entchapter.StateFailed, entchapter.StatePermanentlyFailed,
	} {
		if got := series.EarlyAccessUnlessSettled(state, "", &until); got == nil || !got.Equal(until) {
			t.Errorf("EarlyAccessUnlessSettled(%s) = %v, want %v (this chapter really is waiting)", state, got, until)
		}
	}
}
