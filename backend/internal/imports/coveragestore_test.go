package imports_test

import (
	"context"
	"errors"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/imports"
)

// TestCoverageStoreRoundTrip proves a saved snapshot reads back with its
// scanlator rows intact and a computed_at the caller can render as "as of".
func TestCoverageStoreRoundTrip(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := imports.NewService(nil, nil, client, t.TempDir(), testSearchTimeout, nil)

	want := imports.SourceBreakdownDTO{
		Total: 1301,
		Scanlators: []imports.ScanlatorCoverageDTO{
			{Scanlator: "Alpha", Count: 900, Ranges: "1-900"},
			{Scanlator: "Beta", Count: 401, Ranges: "901-1301"},
		},
	}
	if err := imports.ExportSaveCoverage(svc, ctx, "42", "/qly0d-apotheosis", want); err != nil {
		t.Fatalf("saveCoverage: %v", err)
	}

	got, ok, err := imports.ExportLoadCoverage(svc, ctx, "42", "/qly0d-apotheosis")
	if err != nil || !ok {
		t.Fatalf("loadCoverage: ok=%v err=%v", ok, err)
	}
	if got.Status != "ready" {
		t.Errorf("Status = %q, want ready", got.Status)
	}
	if got.ComputedAt == nil {
		t.Error("ComputedAt = nil, want a timestamp — the owner needs an as-of")
	}
	if got.Payload.Total != 1301 || len(got.Payload.Scanlators) != 2 {
		t.Errorf("payload = %+v, want 1301 total across 2 scanlators", got.Payload)
	}
}

// TestCoverageStoreMissIsNotAnError proves a never-computed pair reports
// ok=false rather than an error, so the caller can branch on "compute it"
// without string-matching a not-found.
func TestCoverageStoreMissIsNotAnError(t *testing.T) {
	client := testdb.New(t)
	svc := imports.NewService(nil, nil, client, t.TempDir(), 0, nil)

	_, ok, err := imports.ExportLoadCoverage(svc, context.Background(), "42", "/never-seen")
	if err != nil {
		t.Fatalf("loadCoverage on a miss returned an error: %v", err)
	}
	if ok {
		t.Error("ok = true for a pair that was never computed")
	}
}

// TestCoverageStoreOverwritesInPlace proves a re-computation REPLACES the row
// rather than inserting a second one. The UNIQUE(source_id, manga_url) index is
// the structural guarantee (Rule 1's dedup discipline); this pins that the
// service actually upserts against it instead of erroring on the conflict.
func TestCoverageStoreOverwritesInPlace(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := imports.NewService(nil, nil, client, t.TempDir(), 0, nil)

	first := imports.SourceBreakdownDTO{Total: 10}
	second := imports.SourceBreakdownDTO{Total: 20}
	if err := imports.ExportSaveCoverage(svc, ctx, "42", "/x", first); err != nil {
		t.Fatalf("save 1: %v", err)
	}
	if err := imports.ExportSaveCoverage(svc, ctx, "42", "/x", second); err != nil {
		t.Fatalf("save 2: %v", err)
	}

	got, _, err := imports.ExportLoadCoverage(svc, ctx, "42", "/x")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Payload.Total != 20 {
		t.Errorf("Total = %d, want 20 (the second save must overwrite)", got.Payload.Total)
	}
	if n := client.SourceCoverage.Query().CountX(ctx); n != 1 {
		t.Errorf("row count = %d, want 1 (overwrite in place, never a second row)", n)
	}
}

// TestCoverageStoreFailureIsVisible proves a failed computation records WHY.
// An empty panel with no reason is the outcome this avoids.
func TestCoverageStoreFailureIsVisible(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := imports.NewService(nil, nil, client, t.TempDir(), 0, nil)

	if err := imports.ExportFailCoverage(svc, ctx, "42", "/x", errors.New("upstream timed out")); err != nil {
		t.Fatalf("failCoverage: %v", err)
	}
	got, ok, err := imports.ExportLoadCoverage(svc, ctx, "42", "/x")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Status != "failed" || got.LastError == "" {
		t.Errorf("Status=%q LastError=%q, want failed with a reason", got.Status, got.LastError)
	}
}

// TestCoverageStoreFailAfterSuccessClearsComputedAt proves a failure that
// follows a PREVIOUSLY SUCCESSFUL saveCoverage does not leave computed_at
// pointing at the old success's as-of. computed_at is the as-of of the
// STORED PAYLOAD; a failed run has no payload, so a stale-but-plausible
// timestamp next to a failed status is worse than no timestamp at all — it
// misleads the owner into thinking the failure is recent-and-otherwise-fine.
func TestCoverageStoreFailAfterSuccessClearsComputedAt(t *testing.T) {
	client := testdb.New(t)
	ctx := context.Background()
	svc := imports.NewService(nil, nil, client, t.TempDir(), 0, nil)

	if err := imports.ExportSaveCoverage(svc, ctx, "42", "/x", imports.SourceBreakdownDTO{Total: 10}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := imports.ExportFailCoverage(svc, ctx, "42", "/x", errors.New("upstream timed out")); err != nil {
		t.Fatalf("failCoverage: %v", err)
	}

	got, ok, err := imports.ExportLoadCoverage(svc, ctx, "42", "/x")
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.Status != "failed" {
		t.Errorf("Status = %q, want failed", got.Status)
	}
	if got.ComputedAt != nil {
		t.Errorf("ComputedAt = %v, want nil — a failed run must not carry the PREVIOUS success's stale as-of", *got.ComputedAt)
	}
}
