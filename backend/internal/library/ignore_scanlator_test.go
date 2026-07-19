package library_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/ingest"
	"github.com/technobecet/tsundoku/internal/library"
	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sse"
)

// fakeIgnoreStore is an in-memory ingest.IgnoreScanlatorStore for the library
// layer tests: `flagged` is the set of source ids flagged ignore-scanlator.
type fakeIgnoreStore struct {
	flagged map[int64]bool
}

func (f fakeIgnoreStore) IgnoreScanlatorSet(context.Context) (map[int64]bool, error) {
	return f.flagged, nil
}

// newIgnoreService builds a library.Service whose ingest is wired with the
// ignore-scanlator flag store, so AddProvider / MatchDiskProvider exercise the
// per-source collapse.
func newIgnoreService(client *ent.Client, storage string, fake *fakeAddProviderClient, flagged map[int64]bool) *library.Service {
	ingestSvc := ingest.NewIngest(fake, client).WithIgnoreScanlator(fakeIgnoreStore{flagged: flagged})
	seriesSvc := series.NewService(client, storage, 14)
	return library.NewService(client, ingestSvc, nil, seriesSvc, func() {}, storage, sse.NewHub())
}

// assertOneLiveProviderCollapsed fails unless there is exactly one live provider
// row for source "1" AND it is the collapsed scanlator="" row at wantImportance
// (no orphan per-uploader row left behind).
func assertOneLiveProviderCollapsed(t *testing.T, ctx context.Context, client *ent.Client, wantImportance int) {
	t.Helper()
	rows := client.SeriesProvider.Query().Where(seriesprovider.Provider("1")).AllX(ctx)
	if len(rows) != 1 {
		t.Fatalf("live provider rows for source \"1\": got %d, want 1 (flagged source must collapse — no orphan)", len(rows))
	}
	if rows[0].Scanlator != "" {
		t.Errorf("collapsed provider Scanlator = %q, want \"\"", rows[0].Scanlator)
	}
	if rows[0].Importance != wantImportance {
		t.Errorf("collapsed provider Importance = %d, want %d (must land on the collapsed row)", rows[0].Importance, wantImportance)
	}
}

// TestAddProvider_IgnoreScanlator_CollapsesStaleScanlator proves MUST-FIX 1's
// AddProvider sibling: attaching a FLAGGED source under a stale non-empty
// scanlator ("Admin") collapses to the single scanlator="" provider at the
// requested importance — no error, no orphan "Admin" row (the dup-check, the
// ingest, and the post-ingest lookup all agree on the collapsed key).
func TestAddProvider_IgnoreScanlator_CollapsesStaleScanlator(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	// A bare series with no providers — isolates the collapse from merge-at-attach.
	ser := client.Series.Create().SetTitle("Flagged Series").SetSlug("flagged-series").SaveX(ctx)

	svc := newIgnoreService(client, storage, newFakeClientWithFeed(t), map[int64]bool{1: true})

	dto, err := svc.AddProvider(ctx, ser.ID, "1", "/manga/99", 7, "Admin")
	if err != nil {
		t.Fatalf("AddProvider (flagged, stale scanlator): %v", err)
	}
	if len(dto.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (the single collapsed provider)", len(dto.Providers))
	}
	assertOneLiveProviderCollapsed(t, ctx, client, 7)
}

// TestMatchDiskProvider_IgnoreScanlator_CollapsesStaleScanlator is the exact
// MUST-FIX 1 regression proof: matching a disk group to a FLAGGED source under a
// stale non-empty scanlator ("Admin") must succeed (no "ent: not found" from the
// post-ingest lookup) and leave the single collapsed scanlator="" provider at
// the requested importance — never an orphan "" row alongside a failed "Admin"
// lookup.
func TestMatchDiskProvider_IgnoreScanlator_CollapsesStaleScanlator(t *testing.T) {
	storage := t.TempDir()
	client := testdb.New(t)
	ctx := context.Background()

	ser, diskSP := setupMatchFixture(t, client, storage)
	svc := newIgnoreService(client, storage, newFakeClientWithFeed(t), map[int64]bool{1: true})

	dto, err := svc.MatchDiskProvider(ctx, ser.ID, diskSP.ID, "1", "/manga/99", "Admin", 5)
	if err != nil {
		t.Fatalf("MatchDiskProvider (flagged, stale scanlator): %v", err)
	}
	// The disk provider is folded into the collapsed live source → one provider.
	if len(dto.Providers) != 1 {
		t.Fatalf("providers = %d, want 1 (disk folded into the collapsed live source)", len(dto.Providers))
	}
	assertOneLiveProviderCollapsed(t, ctx, client, 5)

	// The disk provider row is gone (drained + deleted by the merge).
	if _, err := client.SeriesProvider.Get(ctx, diskSP.ID); !ent.IsNotFound(err) {
		t.Errorf("disk provider still present after match: err = %v, want not-found", err)
	}
}
