package imports_test

import (
	"slices"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/imports"
)

// TestCoverageNeedsComputeAdmissionRule table-drives the one decision that
// separates "serve what is stored" from "start a ~20-minute WebView walk"
// (GAP-140 final review, findings 1 and 2).
//
// It is a pure unit test on purpose: the two bounds it has to straddle are 15
// and 30 MINUTES, so the only honest way to exercise both sides of each is to
// hand the rule a synthetic `now`. Driving them through a real walk would mean
// either a half-hour test or a clock seam in production code that exists
// solely for the test.
func TestCoverageNeedsComputeAdmissionRule(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }

	cases := []struct {
		name  string
		snap  imports.CoverageSnapshot
		ok    bool
		force bool
		want  bool
		why   string
	}{
		{
			name: "never computed",
			ok:   false,
			want: true,
			why:  "nothing is stored, so there is nothing to serve",
		},
		{
			name: "ready",
			snap: imports.CoverageSnapshot{Status: "ready", UpdatedAt: ago(72 * time.Hour)},
			ok:   true,
			want: false,
			why:  "a completed walk must make every later view free, however old — staleness is SURFACED via computedAt, not managed",
		},
		{
			name: "pending, walk still running",
			snap: imports.CoverageSnapshot{Status: "pending", UpdatedAt: ago(5 * time.Minute)},
			ok:   true,
			want: false,
			why:  "a second walk for the same pair is two 20-minute walks against one source, not a cache miss filled twice",
		},
		{
			name: "pending, just under the stale bound",
			snap: imports.CoverageSnapshot{Status: "pending", UpdatedAt: ago(29 * time.Minute)},
			ok:   true,
			want: false,
			why:  "still inside the slowest observed walk's envelope — restarting here would duplicate a LIVE walk",
		},
		{
			name: "pending, past the stale bound",
			snap: imports.CoverageSnapshot{Status: "pending", UpdatedAt: ago(31 * time.Minute)},
			ok:   true,
			want: true,
			why:  "nothing rewrites the row when the process dies mid-walk; without this the owner sits on Computing… forever",
		},
		{
			name: "failed, fresh",
			snap: imports.CoverageSnapshot{Status: "failed", UpdatedAt: ago(1 * time.Second)},
			ok:   true,
			want: false,
			why:  "THE loop: a recompute here fails, announces, the screen re-fetches, and it recomputes again, forever",
		},
		{
			name: "failed, just under the cooldown",
			snap: imports.CoverageSnapshot{Status: "failed", UpdatedAt: ago(14 * time.Minute)},
			ok:   true,
			want: false,
			why:  "the cooldown has to hold across the whole fail/announce/re-fetch cycle, not just its first round trip",
		},
		{
			name: "failed, past the cooldown",
			snap: imports.CoverageSnapshot{Status: "failed", UpdatedAt: ago(16 * time.Minute)},
			ok:   true,
			want: true,
			why:  "there is no refresh affordance in the UI, so a permanent failure would cost the owner the series for good",
		},
		{
			name: "an unrecognised status",
			snap: imports.CoverageSnapshot{Status: "quiescent", UpdatedAt: now},
			ok:   true,
			want: true,
			why:  "fail-safe: recomputing is merely wasteful, serving an uninterpretable row as authoritative is wrong",
		},
		{
			name:  "ready, but the owner asked for a refresh",
			snap:  imports.CoverageSnapshot{Status: "ready", UpdatedAt: ago(1 * time.Hour)},
			ok:    true,
			force: true,
			want:  true,
			why:   "a `ready` snapshot must stay recomputable forever when the owner explicitly asks — it must not freeze counts permanently",
		},
		{
			name:  "failed and fresh, but the owner asked for a refresh",
			snap:  imports.CoverageSnapshot{Status: "failed", UpdatedAt: ago(1 * time.Second)},
			ok:    true,
			force: true,
			want:  true,
			why:   "an explicit refresh must bypass the failed-cooldown too — the owner asked for a recomputation right now, not later",
		},
		{
			name:  "pending and LIVE, refresh must NOT duplicate the walk",
			snap:  imports.CoverageSnapshot{Status: "pending", UpdatedAt: ago(5 * time.Minute)},
			ok:    true,
			force: true,
			want:  false,
			why:   "a refresh arriving while a walk is genuinely in flight must join that walk, not start a second ~20-minute WebView walk against the same source — this is the ONE guard force cannot bypass",
		},
		{
			name:  "pending and stale, refresh restarts it (same as without force)",
			snap:  imports.CoverageSnapshot{Status: "pending", UpdatedAt: ago(31 * time.Minute)},
			ok:    true,
			force: true,
			want:  true,
			why:   "a dead process's stale claim is restarted whether or not the owner asked explicitly",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := imports.ExportCoverageNeedsCompute(tc.snap, tc.ok, now, tc.force)
			if got != tc.want {
				t.Errorf("coverageNeedsCompute = %v, want %v — %s", got, tc.want, tc.why)
			}
		})
	}
}

// TestCoverageAfterComputeNeverYieldsAnEmptyStatus pins GAP-140 final review
// finding 4: a computation that finished but left NO row behind (its very
// first store write failed while reads still worked) used to be returned as
// the zero CoverageSnapshot, whose Status is "" — not a member of the wire
// enum. It reached the client as {"status":""}, and the scan-library row
// renders that as NOTHING at all: no counts, no "Computing…", no
// "unavailable". A blank row is the single worst outcome here, because it is
// indistinguishable from a UI bug.
//
// This is a pure unit test because the condition it covers — the store
// refusing WRITES while still serving READS — has no seam in the harness.
// testdb can close the connection, but that kills reads too, which Coverage
// reports as an outright error rather than an absent row. Manufacturing it
// would mean adding a write-failure injection point to production code, which
// the repo's test rules explicitly rule out.
func TestCoverageAfterComputeNeverYieldsAnEmptyStatus(t *testing.T) {
	valid := imports.ExportCoverageStatuses()

	got := imports.ExportCoverageAfterCompute(imports.CoverageSnapshot{}, false)

	if got.Status == "" {
		t.Fatal(`Status = "" for an absent row — this ships as {"status":""} and renders as a blank row`)
	}
	if !slices.Contains(valid, got.Status) {
		t.Fatalf("Status = %q, which is not one of the wire enum values %v", got.Status, valid)
	}
	if got.Status != "failed" {
		t.Errorf("Status = %q, want failed — the computation is OVER and produced nothing readable, so `pending` would spin the UI on a walk that will never report again", got.Status)
	}
	if got.LastError == "" {
		t.Error("LastError is empty — the owner would see an unexplained failure, which is the empty-panel outcome this snapshot is persisted to avoid")
	}
}

// TestCoverageAfterComputePassesAPresentRowThrough proves the guard above is
// narrow: a row that IS present is returned verbatim, never rewritten to
// `failed`. Without this the previous test would still pass if the function
// simply always reported failure.
func TestCoverageAfterComputePassesAPresentRowThrough(t *testing.T) {
	want := imports.CoverageSnapshot{Status: "ready", Payload: imports.SourceBreakdownDTO{Total: 1301}}

	got := imports.ExportCoverageAfterCompute(want, true)

	if got.Status != "ready" || got.Payload.Total != 1301 {
		t.Errorf("snapshot = %+v, want the stored row passed through untouched", got)
	}
}
