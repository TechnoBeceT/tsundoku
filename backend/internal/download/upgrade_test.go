// Package download_test contains integration tests for the upgrade engine.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/sse"
)

// assertUpgradeProvenance validates that a chapter's provenance fields reflect
// the expected post-upgrade values (§16 full-payload).
func assertUpgradeProvenance(t *testing.T, ch *ent.Chapter, wantProviderID uuid.UUID, wantImportance int) {
	t.Helper()
	if ch.State != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded, got %s", ch.State)
	}
	if ch.Filename == "" {
		t.Error("filename must be set after upgrade")
	}
	if ch.PageCount == nil || *ch.PageCount == 0 {
		t.Error("page_count must be set after upgrade")
	}
	if ch.SatisfiedImportance == nil || *ch.SatisfiedImportance != wantImportance {
		t.Errorf("satisfied_importance: want %d, got %v", wantImportance, ch.SatisfiedImportance)
	}
	if ch.SatisfiedByProviderID == nil || *ch.SatisfiedByProviderID != wantProviderID {
		t.Errorf("satisfied_by_provider_id: want %s, got %v", wantProviderID, ch.SatisfiedByProviderID)
	}
}

// assertOriginalPreserved checks that neither the file bytes nor the DB
// provenance changed after a failed upgrade.
func assertOriginalPreserved(
	t *testing.T,
	ch *ent.Chapter,
	originalFilename string,
	originalProviderID uuid.UUID,
	originalImportance int,
	originalBytes []byte,
	originalPath string,
) {
	t.Helper()
	if ch.State != entchapter.StateDownloaded {
		t.Errorf("state after failed upgrade: want downloaded, got %s", ch.State)
	}
	if ch.SatisfiedByProviderID == nil || *ch.SatisfiedByProviderID != originalProviderID {
		t.Errorf("satisfied_by_provider_id: want %s (unchanged), got %v", originalProviderID, ch.SatisfiedByProviderID)
	}
	if ch.SatisfiedImportance == nil || *ch.SatisfiedImportance != originalImportance {
		t.Errorf("satisfied_importance: want %d (unchanged), got %v", originalImportance, ch.SatisfiedImportance)
	}
	if ch.Filename != originalFilename {
		t.Errorf("filename: want %q (unchanged), got %q", originalFilename, ch.Filename)
	}
	if ch.LastError == "" {
		t.Error("last_error must be set after a failed upgrade")
	}
	currentBytes, err := os.ReadFile(originalPath) //nolint:gosec // test-only; path is constructed from t.TempDir
	if err != nil {
		t.Fatalf("original CBZ missing after failed upgrade: %v", err)
	}
	if string(currentBytes) != string(originalBytes) {
		t.Error("original CBZ bytes changed after failed upgrade (non-destructive invariant violated)")
	}
}

// collectSSEEvents drains events from ch until n have been received or timeout
// elapses. It is a generic helper used across SSE-asserting tests.
func collectSSEEvents(events <-chan sse.Event, n int, timeout time.Duration) []sse.Event {
	var got []sse.Event
	timer := time.After(timeout)
	for len(got) < n {
		select {
		case ev, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-timer:
			return got
		}
	}
	return got
}

// TestUpgrade_SwapsFile is the Bug #2 cure: a chapter downloaded at
// importance=2 gets a new importance=5 provider for the same key.
// DetectUpgrades must flag it upgrade_available, and Upgrade must swap the
// file and provenance atomically so the chapter ends in state=downloaded with
// satisfied_importance==5.
func TestUpgrade_SwapsFile(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Upgrade Series").SetSlug("upgrade-series").SaveX(ctx)
	spLow := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spLow.ID).SetChapterKey("ch-upg").
		SetURL("https://low.example.com/ch-upg").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-upg").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 1, MaxRetries: 3, Storage: storageDir,
	})
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	initial := client.Chapter.GetX(ctx, ch.ID)
	if initial.State != entchapter.StateDownloaded {
		t.Fatalf("initial state: want downloaded, got %s", initial.State)
	}
	oldFilename := initial.Filename
	oldPath := filepath.Join(storageDir, "Other", "Upgrade Series", oldFilename)

	// Add a higher-importance provider for the same chapter key.
	spHigh := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-high").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).SetChapterKey("ch-upg").
		SetURL("https://high.example.com/ch-upg").SetProviderIndex(0).SaveX(ctx)

	n, err := download.DetectUpgrades(ctx, client)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 1 {
		t.Errorf("DetectUpgrades: want 1 flagged, got %d", n)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateUpgradeAvailable {
		t.Errorf("after DetectUpgrades: want upgrade_available, got %s", client.Chapter.GetX(ctx, ch.ID).State)
	}

	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)
	assertUpgradeProvenance(t, final, spHigh.ID, 5)

	// New CBZ must exist on disk.
	newPath := filepath.Join(storageDir, "Other", "Upgrade Series", final.Filename)
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("new CBZ not found at %s: %v", newPath, err)
	}

	// If the filename changed (different provider ⇒ different name), the old file
	// must have been cleaned up by Upgrade.
	if oldFilename != final.Filename {
		if _, statErr := os.Stat(oldPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("old CBZ should have been deleted after filename change: stat(%s) = %v", oldPath, statErr)
		}
	}
}

// TestUpgrade_NonDestructiveOnFailure verifies the non-destructive guarantee:
// when the upgrade fetch fails, the original CBZ and provenance remain intact
// and the chapter returns to state=downloaded.
func TestUpgrade_NonDestructiveOnFailure(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Failsafe Series").SetSlug("failsafe-series").SaveX(ctx)
	spLow := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spLow.ID).SetChapterKey("ch-fail").
		SetURL("https://low.example.com/ch-fail").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-fail").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 1, MaxRetries: 3, Storage: storageDir,
	})
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	initial := client.Chapter.GetX(ctx, ch.ID)
	if initial.State != entchapter.StateDownloaded {
		t.Fatalf("initial state: want downloaded, got %s", initial.State)
	}
	originalFilename := initial.Filename
	originalPath := filepath.Join(storageDir, "Other", "Failsafe Series", originalFilename)
	originalBytes, err := os.ReadFile(originalPath) //nolint:gosec // test-only; path is constructed from t.TempDir
	if err != nil {
		t.Fatalf("read original CBZ: %v", err)
	}

	// Add a high-importance provider so DetectUpgrades flags the chapter.
	spHigh := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-high").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).SetChapterKey("ch-fail").
		SetURL("https://high.example.com/ch-fail").SetProviderIndex(0).SaveX(ctx)

	if _, err := download.DetectUpgrades(ctx, client); err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}

	// Upgrade with a fetcher that always errors — must be non-destructive.
	dFail := download.New(client, fake.New(fake.WithError(errors.New("simulated fetch failure"))), hub,
		download.Config{PerProviderConcurrency: 1, MaxRetries: 3, Storage: storageDir})
	if err := dFail.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade returned unexpected hard error: %v", err)
	}

	assertOriginalPreserved(t, client.Chapter.GetX(ctx, ch.ID),
		originalFilename, spLow.ID, 2, originalBytes, originalPath)
}

// TestDetectUpgrades_StrictlyGreater verifies that the upgrade rule is a
// strict > comparison: a chapter already satisfied at importance=5 with
// another provider also at importance=5 must NOT be flagged. This test FAILS
// if the comparison is >= instead of >.
func TestDetectUpgrades_StrictlyGreater(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Strict Series").SetSlug("strict-series").SaveX(ctx)
	sp := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-equal").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(sp.ID).SetChapterKey("ch-strict").
		SetURL("https://equal.example.com/ch-strict").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-strict").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 1, MaxRetries: 3, Storage: storageDir,
	})
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateDownloaded {
		t.Fatal("chapter should be downloaded before DetectUpgrades")
	}

	// DetectUpgrades must return 0 — equal importance must NOT trigger an upgrade.
	n, err := download.DetectUpgrades(ctx, client)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 0 {
		t.Errorf("DetectUpgrades: want 0 (equal importance is not an upgrade), got %d", n)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateDownloaded {
		t.Error("state must remain downloaded when no strictly-higher provider exists")
	}
}

// TestUpgrade_SSEEvents verifies that a successful upgrade emits an
// "upgrade.start" event (transitioning to upgrading) followed by a
// "download.done" event (back to downloaded).
func TestUpgrade_SSEEvents(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("SSE Upg Series").SetSlug("sse-upg-series").SaveX(ctx)
	spLow := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spLow.ID).SetChapterKey("ch-sse-upg").
		SetURL("https://low.example.com/ch-sse-upg").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-sse-upg").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		PerProviderConcurrency: 1, MaxRetries: 3, Storage: storageDir,
	})
	if err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}

	spHigh := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-high").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).SetChapterKey("ch-sse-upg").
		SetURL("https://high.example.com/ch-sse-upg").SetProviderIndex(0).SaveX(ctx)
	if _, err := download.DetectUpgrades(ctx, client); err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}

	// Subscribe before the upgrade so we capture its events.
	events, unsub := hub.Subscribe()
	defer unsub()

	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	got := collectSSEEvents(events, 2, 2*time.Second)
	if len(got) < 2 {
		t.Fatalf("want at least 2 SSE events (upgrade.start + download.done), got %d", len(got))
	}
	if got[0].Type != "upgrade.start" {
		t.Errorf("first event: want upgrade.start, got %q", got[0].Type)
	}
	if got[1].Type != "download.done" {
		t.Errorf("second event: want download.done, got %q", got[1].Type)
	}
}
