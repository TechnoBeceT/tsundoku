// Package download_test contains integration tests for the upgrade engine.
// Tests require Docker (via testcontainers) for an ephemeral PostgreSQL instance.
package download_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
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
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
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

	n, err := download.DetectUpgrades(ctx, client, 3)
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

// TestUpgrade_RemovesDuplicateCBZsOnConvergence proves the Task 5 wiring: on a
// successful upgrade of a NUMBERED chapter, Upgrade removes EVERY other CBZ that
// shares the chapter number — the previous winner AND any pre-existing orphan
// duplicate — keeping only the new winning file (one file per chapter number).
func TestUpgrade_RemovesDuplicateCBZsOnConvergence(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Dedup Series").SetSlug("dedup-series").SaveX(ctx)
	spLow := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spLow.ID).SetChapterKey("10").SetNumber(10).
		SetURL("https://low.example.com/10").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("10").SetNumber(10).SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	initial := client.Chapter.GetX(ctx, ch.ID)
	if initial.State != entchapter.StateDownloaded {
		t.Fatalf("initial state: want downloaded, got %s", initial.State)
	}
	seriesDir := filepath.Join(storageDir, "Other", "Dedup Series")

	// Plant a pre-existing ORPHAN duplicate CBZ for the same chapter number.
	orphanPath := filepath.Join(seriesDir, "[orphan] Dedup Series 10.cbz")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0o600); err != nil {
		t.Fatalf("plant orphan CBZ: %v", err)
	}

	// Add a higher-importance provider for the same chapter and converge.
	spHigh := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-high").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).SetChapterKey("10").SetNumber(10).
		SetURL("https://high.example.com/10").SetProviderIndex(0).SaveX(ctx)
	if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)
	assertUpgradeProvenance(t, final, spHigh.ID, 5)

	// The planted orphan (same number) must be gone.
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("planted orphan duplicate should have been removed on convergence: stat = %v", err)
	}

	// Exactly ONE .cbz must remain for chapter 10 — the new winner.
	assertOnlyCBZ(t, seriesDir, final.Filename)
}

// assertOnlyCBZ fails unless dir contains exactly one .cbz file named want.
func assertOnlyCBZ(t *testing.T, dir, want string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %q: %v", dir, err)
	}
	var cbzs []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".cbz" {
			cbzs = append(cbzs, e.Name())
		}
	}
	if len(cbzs) != 1 || cbzs[0] != want {
		t.Errorf("remaining CBZs = %v, want exactly [%s]", cbzs, want)
	}
}

// TestUpgrade_ScanlatorChangeReplacesFile proves the one-file-per-number
// invariant holds when an upgrade changes ONLY the scanlator (same provider).
// Since the CBZ filename now encodes "[Provider-Scanlator]" (Task 5), a
// same-provider scanlator swap still changes the filename, so the old CBZ must
// be deleted and exactly one CBZ must remain for the chapter's number.
func TestUpgrade_ScanlatorChangeReplacesFile(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Scanlator Swap").SetSlug("scanlator-swap").SaveX(ctx)
	spBeta := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("comix").SetScanlator("Beta").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spBeta.ID).SetChapterKey("ch-scan").
		SetURL("https://beta.example.com/ch-scan").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-scan").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	initial := client.Chapter.GetX(ctx, ch.ID)
	if initial.State != entchapter.StateDownloaded {
		t.Fatalf("initial state: want downloaded, got %s", initial.State)
	}
	oldFilename := initial.Filename
	seriesDir := filepath.Join(storageDir, "Other", "Scanlator Swap")
	oldPath := filepath.Join(seriesDir, oldFilename)
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("initial CBZ not found: %v", err)
	}

	// Same provider ("comix"), a DIFFERENT scanlator, at a strictly higher
	// importance — this is the swap DetectUpgrades must flag.
	spAlpha := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("comix").SetScanlator("Alpha").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spAlpha.ID).SetChapterKey("ch-scan").
		SetURL("https://alpha.example.com/ch-scan").SetProviderIndex(0).SaveX(ctx)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 1 {
		t.Fatalf("DetectUpgrades: want 1 flagged, got %d", n)
	}

	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)
	assertUpgradeProvenance(t, final, spAlpha.ID, 5)
	assertScanlatorSwapCleanedUp(t, seriesDir, oldFilename, oldPath, final.Filename)
}

// assertScanlatorSwapCleanedUp asserts that a same-provider scanlator swap
// changed the filename, wrote the new CBZ, deleted the old one, and left
// exactly one .cbz in seriesDir (the one-file-per-number invariant).
func assertScanlatorSwapCleanedUp(t *testing.T, seriesDir, oldFilename, oldPath, newFilename string) {
	t.Helper()

	if newFilename == oldFilename {
		t.Fatalf("filename unchanged after scanlator swap: %q — the bracket must encode the scanlator", newFilename)
	}

	newPath := filepath.Join(seriesDir, newFilename)
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("new CBZ not found at %s: %v", newPath, err)
	}
	if _, statErr := os.Stat(oldPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("old CBZ should have been deleted after scanlator swap: stat(%s) = %v", oldPath, statErr)
	}

	// One-file-per-number: exactly one .cbz must remain in the series dir.
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", seriesDir, err)
	}
	cbzCount := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".cbz" {
			cbzCount++
		}
	}
	if cbzCount != 1 {
		t.Errorf("cbz count after scanlator swap = %d, want 1 (one-file-per-number)", cbzCount)
	}
}

// TestUpgrade_NonOtherCategoryUsesRealFolder is the Part C regression proof: a
// series filed under a REAL category ("Manhwa") must have its CBZ rendered AND
// its old CBZ deleted under that category's folder — never the hardcoded "Other".
//
// Before the fix, buildRenderMeta wrote to <storage>/Manhwa/… (correct, it read
// the category edge) but tryDeleteOldCBZ looked under <storage>/Other/… (wrong),
// so upgrading a non-Other series orphaned the old CBZ. Now both resolve the real
// category via the shared seriesCategoryName, so they agree.
func TestUpgrade_NonOtherCategoryUsesRealFolder(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	// A series filed under the seeded "Manhwa" category (NOT the default Other).
	manhwaID, err := category.IDByName(ctx, client, "Manhwa")
	if err != nil {
		t.Fatalf("IDByName(Manhwa): %v", err)
	}
	s := client.Series.Create().
		SetTitle("Solo Leveling").SetSlug("solo-leveling").SetCategoryID(manhwaID).SaveX(ctx)
	spLow := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-low").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spLow.ID).SetChapterKey("ch-manhwa").
		SetURL("https://low.example.com/ch-manhwa").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-manhwa").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	initial := client.Chapter.GetX(ctx, ch.ID)
	oldFilename := initial.Filename

	// The initial CBZ must be under the Manhwa folder, not Other.
	oldPath := filepath.Join(storageDir, "Manhwa", "Solo Leveling", oldFilename)
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("initial CBZ not under Manhwa: %v", err)
	}

	// Upgrade with a higher-importance provider (different filename ⇒ delete path).
	spHigh := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-high").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).SetChapterKey("ch-manhwa").
		SetURL("https://high.example.com/ch-manhwa").SetProviderIndex(0).SaveX(ctx)
	if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	final := client.Chapter.GetX(ctx, ch.ID)

	// New CBZ is under Manhwa.
	newPath := filepath.Join(storageDir, "Manhwa", "Solo Leveling", final.Filename)
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("new CBZ not under Manhwa at %s: %v", newPath, err)
	}
	// The old CBZ (under Manhwa) was deleted — the whole point of the fix.
	if oldFilename != final.Filename {
		if _, statErr := os.Stat(oldPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("old CBZ under Manhwa should have been deleted: stat(%s) = %v", oldPath, statErr)
		}
	}
	// Nothing must have been rendered under the hardcoded "Other".
	if _, statErr := os.Stat(filepath.Join(storageDir, "Other")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("no files should exist under Other for a Manhwa series; stat err = %v", statErr)
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
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
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

	if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}

	// Upgrade with a fetcher that always errors — must be non-destructive.
	dFail := download.New(client, fake.New(fake.WithError(errors.New("simulated fetch failure"))), hub,
		download.Config{Storage: storageDir}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if err := dFail.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade returned unexpected hard error: %v", err)
	}

	assertOriginalPreserved(t, client.Chapter.GetX(ctx, ch.ID),
		originalFilename, spLow.ID, 2, originalBytes, originalPath)
}

// TestDetectUpgrades_StrictlyGreater verifies the strict > comparison rule.
//
// Two cases are tested:
//  1. Equal-importance alternative: a second provider at the same importance=5
//     exists for the same chapter key. DetectUpgrades must return 0 and the
//     chapter must remain downloaded — an equal-importance source is NOT an
//     upgrade (guards against an accidental >= regression).
//  2. Strictly-higher provider: adding a third provider at importance=6 must
//     cause DetectUpgrades to return 1 and transition the chapter to
//     upgrade_available (guards against an accidental <= regression).
func TestDetectUpgrades_StrictlyGreater(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Strict Series").SetSlug("strict-series").SaveX(ctx)

	// Provider that will satisfy the initial download (importance=5).
	spA := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-a").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spA.ID).SetChapterKey("ch-strict").
		SetURL("https://a.example.com/ch-strict").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-strict").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateDownloaded {
		t.Fatal("chapter should be downloaded before DetectUpgrades")
	}

	// Case 1: add a second provider at the SAME importance=5 (different provider,
	// same chapter key). DetectUpgrades must return 0 — equal importance is not
	// an upgrade, so the strict > rule must hold.
	spB := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-b").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spB.ID).SetChapterKey("ch-strict").
		SetURL("https://b.example.com/ch-strict").SetProviderIndex(0).SaveX(ctx)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (equal-importance case): %v", err)
	}
	if n != 0 {
		t.Errorf("DetectUpgrades (equal-importance): want 0, got %d — equal importance must NOT trigger an upgrade", n)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateDownloaded {
		t.Error("state must remain downloaded when only equal-importance alternatives exist")
	}

	// Case 2: add a STRICTLY higher-importance provider (importance=6). Now
	// DetectUpgrades must return 1 and the chapter must be flagged upgrade_available.
	spC := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-c").SetImportance(6).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spC.ID).SetChapterKey("ch-strict").
		SetURL("https://c.example.com/ch-strict").SetProviderIndex(0).SaveX(ctx)

	n, err = download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades (strictly-higher case): %v", err)
	}
	if n != 1 {
		t.Errorf("DetectUpgrades (strictly-higher): want 1, got %d — a strictly higher provider must trigger an upgrade", n)
	}
	if client.Chapter.GetX(ctx, ch.ID).State != entchapter.StateUpgradeAvailable {
		t.Errorf("state after strictly-higher detect: want upgrade_available, got %s", client.Chapter.GetX(ctx, ch.ID).State)
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
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}

	spHigh := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-high").SetImportance(5).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spHigh.ID).SetChapterKey("ch-sse-upg").
		SetURL("https://high.example.com/ch-sse-upg").SetProviderIndex(0).SaveX(ctx)
	if _, err := download.DetectUpgrades(ctx, client, 3); err != nil {
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

// TestDetectUpgrades_SkipsWhenBestIsCurrentSatisfier is the self-churn cure: a
// downloaded chapter whose ONLY live source is the one that already satisfies it
// must NOT be flagged for upgrade even when that source's importance has since
// been raised above the frozen satisfied_importance watermark. Re-fetching from
// the same source would be pure churn. DetectUpgrades instead refreshes the
// stale watermark to the source's current importance and leaves the chapter
// downloaded.
func TestDetectUpgrades_SkipsWhenBestIsCurrentSatisfier(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Self Sat Series").SetSlug("self-sat-series").SaveX(ctx)
	spP := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-p").SetImportance(1).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spP.ID).SetChapterKey("ch-self").
		SetURL("https://p.example.com/ch-self").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-self").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	downloaded := client.Chapter.GetX(ctx, ch.ID)
	if downloaded.State != entchapter.StateDownloaded {
		t.Fatalf("initial state: want downloaded, got %s", downloaded.State)
	}
	if downloaded.SatisfiedImportance == nil || *downloaded.SatisfiedImportance != 1 {
		t.Fatalf("initial satisfied_importance: want 1, got %v", downloaded.SatisfiedImportance)
	}

	// Raise the SAME (and only) source's importance well above the frozen
	// watermark. The old maxImportanceForChapter model would re-fire an upgrade
	// from this very source; the fixed model must not.
	client.SeriesProvider.UpdateOneID(spP.ID).SetImportance(5).ExecX(ctx)

	n, err := download.DetectUpgrades(ctx, client, 3)
	if err != nil {
		t.Fatalf("DetectUpgrades: %v", err)
	}
	if n != 0 {
		t.Errorf("DetectUpgrades: want 0 flagged (best source IS the current satisfier), got %d", n)
	}

	after := client.Chapter.GetX(ctx, ch.ID)
	if after.State != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded (no upgrade), got %s", after.State)
	}
	if after.SatisfiedImportance == nil || *after.SatisfiedImportance != 5 {
		t.Errorf("satisfied_importance: want 5 (watermark refreshed to the source's current importance), got %v", after.SatisfiedImportance)
	}
}

// TestUpgrade_NoOpWhenBestIsCurrentSatisfier is the defence-in-depth partner to
// the DetectUpgrades guard: even if a chapter somehow carries a STALE
// upgrade_available flag whose only live candidate is its current satisfier,
// Upgrade must NOT fetch or re-render. It refreshes the watermark and returns the
// chapter to downloaded with its file untouched (fetcher never called).
func TestUpgrade_NoOpWhenBestIsCurrentSatisfier(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)
	hub := sse.NewHub()

	s := client.Series.Create().SetTitle("Stale Flag Series").SetSlug("stale-flag-series").SaveX(ctx)
	spP := client.SeriesProvider.Create().
		SetSeries(s).SetProvider("prov-p").SetImportance(2).SaveX(ctx)
	client.ProviderChapter.Create().
		SetSeriesProviderID(spP.ID).SetChapterKey("ch-stale").
		SetURL("https://p.example.com/ch-stale").SetProviderIndex(0).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("ch-stale").SaveX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatalf("initial RunOnce: %v", err)
	}
	downloaded := client.Chapter.GetX(ctx, ch.ID)
	originalFilename := downloaded.Filename
	if originalFilename == "" {
		t.Fatal("expected a filename after the initial download")
	}

	// Force a STALE upgrade_available flag (bypass the state machine) with the
	// satisfier as the only source, and raise its importance so a naive upgrade
	// would re-fetch from it.
	client.SeriesProvider.UpdateOneID(spP.ID).SetImportance(9).ExecX(ctx)
	client.Chapter.UpdateOneID(ch.ID).SetState(entchapter.StateUpgradeAvailable).ExecX(ctx)

	counter := &countingFetcher{base: fake.New()}
	dCount := download.New(client, counter, hub, download.Config{
		Storage: storageDir,
	}, settings.Static{Retries: 3, Backoff: time.Hour}, nil)

	if err := dCount.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade returned unexpected error: %v", err)
	}

	if got := atomic.LoadInt64(&counter.calls); got != 0 {
		t.Errorf("fetcher call count: want 0 (self-satisfier no-op), got %d", got)
	}
	after := client.Chapter.GetX(ctx, ch.ID)
	if after.State != entchapter.StateDownloaded {
		t.Errorf("state: want downloaded (stale flag cleared), got %s", after.State)
	}
	if after.Filename != originalFilename {
		t.Errorf("filename: want %q (unchanged), got %q", originalFilename, after.Filename)
	}
	if after.SatisfiedImportance == nil || *after.SatisfiedImportance != 9 {
		t.Errorf("satisfied_importance: want 9 (watermark refreshed), got %v", after.SatisfiedImportance)
	}
}

// TestUpgradeFailure_CleansStagingDir proves FIX 2: a FAILED upgrade wipes the
// target provider's page-staging dir, so a later upgrade attempt can never pack
// stale index-keyed staged pages against a re-resolved (possibly reordered) page
// list — the silent-corruption shape that would then supersede the good original
// CBZ. It wires the REAL validating Fetcher and a higher-importance source whose
// second page is broken: the fetch fails AFTER staging page 0, and the upgrade
// path must remove that dir on the way out.
func TestUpgradeFailure_CleansStagingDir(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := mustTempDir(t)
	stagingRoot := mustTempDir(t)

	s := client.Series.Create().SetTitle("Upgrade Staging").SetSlug("upgrade-staging").SaveX(ctx)
	// Current satisfier: a disk-origin (low) source.
	spLow := client.SeriesProvider.Create().SetSeries(s).SetProvider("Disk").SetImportance(1).SaveX(ctx)
	// The upgrade target: a numeric (live) higher-importance source.
	spHigh := client.SeriesProvider.Create().SetSeries(s).SetProvider("7").SetImportance(10).SaveX(ctx)
	_ = client.ProviderChapter.Create().SetSeriesProviderID(spLow.ID).SetChapterKey("c1").
		SetURL("/lo/c1").SetProviderIndex(0).SaveX(ctx)
	pcHigh := client.ProviderChapter.Create().SetSeriesProviderID(spHigh.ID).SetChapterKey("c1").
		SetURL("/hi/c1").SetProviderIndex(0).SaveX(ctx)
	// A downloaded chapter satisfied by the low source, already flagged for upgrade.
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("c1").SetNumber(1).
		SetState(entchapter.StateUpgradeAvailable).
		SetSatisfiedByProviderID(spLow.ID).SetSatisfiedImportance(1).
		SetFilename("old.cbz").SaveX(ctx)

	good := encodeTestJPEG(t)
	bc := &brokenPageClient{
		pages: []sourceengine.Page{
			{Index: 0, URL: "u0"},
			{Index: 1, URL: "u1"}, // broken → fetch fails after page 0 is staged
		},
		pageData: map[string][]byte{"u0": good, "u1": good[:12]},
	}
	d := download.New(client, sourceengine.NewFetcher(bc, stagingRoot), sse.NewHub(),
		download.Config{Storage: storage, StagingRoot: stagingRoot},
		settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 1}, nil)

	if err := d.Upgrade(ctx, ch.ID); err != nil {
		t.Fatalf("Upgrade: %v", err)
	}

	// The working copy is retained: the chapter returns to downloaded.
	if st := client.Chapter.GetX(ctx, ch.ID).State; st != entchapter.StateDownloaded {
		t.Fatalf("state = %s, want downloaded (upgrade failure keeps the working copy)", st)
	}
	// And the target's staging dir is GONE — no stale pages for a later attempt.
	stagingDir := filepath.Join(stagingRoot, pcHigh.ID.String())
	if _, err := os.Stat(stagingDir); !os.IsNotExist(err) {
		t.Errorf("staging dir %s survived a failed upgrade (want cleaned); stat err = %v", stagingDir, err)
	}
}
