// Package download_test — tests for DetectSupersededParts, the fractional-part
// suppression detector (superseded.go). Tests require Docker (testcontainers).
package download_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// seedSupersessionSeries creates a series (default "Other" category) under the
// given slug/title and returns (client-visible) helpers for creating chapters.
func seedSupersessionSeries(ctx context.Context, t *testing.T, client *ent.Client, slug, title string) *ent.Series {
	t.Helper()
	return client.Series.Create().
		SetTitle(title).
		SetSlug(slug).
		SetCategoryID(catID(ctx, client, "Other")).
		SaveX(ctx)
}

// createChapter creates a Chapter row directly (no ProviderChapter feed needed —
// DetectSupersededParts only reads Chapter rows) with the given number/state/
// filename. key must be unique per series.
func createChapter(ctx context.Context, t *testing.T, client *ent.Client, s *ent.Series, key string, number float64, state entchapter.State, filename string) *ent.Chapter {
	t.Helper()
	c := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey(key).
		SetNillableNumber(&number).
		SetState(state).
		SaveX(ctx)
	if filename != "" {
		c = client.Chapter.UpdateOneID(c.ID).SetFilename(filename).SaveX(ctx)
	}
	return c
}

// newSupersessionDispatcher builds a Dispatcher with a fake fetcher (unused by
// DetectSupersededParts) and the given suppress flag.
func newSupersessionDispatcher(client *ent.Client, storageDir string, suppress bool) *download.Dispatcher {
	return download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: storageDir},
		settings.Static{Retries: 3, Backoff: time.Hour, SuppressParts: suppress}, nil)
}

func chapterState(ctx context.Context, t *testing.T, client *ent.Client, id uuid.UUID) entchapter.State {
	t.Helper()
	return client.Chapter.GetX(ctx, id).State
}

// assertChapterState fails the test if the given chapter is not in the wanted
// state. label identifies the chapter in the failure message.
func assertChapterState(ctx context.Context, t *testing.T, client *ent.Client, id uuid.UUID, want entchapter.State, label string) {
	t.Helper()
	if got := chapterState(ctx, t, client, id); got != want {
		t.Errorf("%s state = %s, want %s", label, got, want)
	}
}

// runRevertScenario seeds a series with a whole chapter (in wholeState) and two
// already-superseded parts, runs DetectSupersededParts with the given suppress
// flag, and asserts the returned counts plus that both parts land in wanted.
// Shared by TestDetectSupersededParts_RevertsWhenWholeGone (whole no longer
// downloaded) and TestDetectSupersededParts_DisabledRevertsAll (setting
// disabled) — both are "revert everything superseded back to wanted" cases that
// differ only in why the revert fires.
func runRevertScenario(t *testing.T, slug, title string, wholeState entchapter.State, suppress bool) {
	t.Helper()
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)

	s := seedSupersessionSeries(ctx, t, client, slug, title)
	createChapter(ctx, t, client, s, "ch-1", 1, wholeState, "")
	part1 := createChapter(ctx, t, client, s, "ch-1.1", 1.1, entchapter.StateSuperseded, "")
	part2 := createChapter(ctx, t, client, s, "ch-1.2", 1.2, entchapter.StateSuperseded, "")

	d := newSupersessionDispatcher(client, storageDir, suppress)
	superseded, reverted, err := d.DetectSupersededParts(ctx)
	if err != nil {
		t.Fatalf("DetectSupersededParts: %v", err)
	}
	if superseded != 0 {
		t.Errorf("superseded = %d, want 0", superseded)
	}
	if reverted != 2 {
		t.Errorf("reverted = %d, want 2", reverted)
	}
	assertChapterState(ctx, t, client, part1.ID, entchapter.StateWanted, "part 1.1")
	assertChapterState(ctx, t, client, part2.ID, entchapter.StateWanted, "part 1.2")
}

// TestDetectSupersededParts_SupersedesPartsOfDownloadedWhole covers the core
// suppression case: whole 1 downloaded with 2 fractional parts under it (one
// downloaded with an on-disk CBZ, one wanted) both get superseded and the
// downloaded part's file is removed; a lone side-chapter (10.5 under whole 10)
// is never touched; the wholes themselves are untouched.
func TestDetectSupersededParts_SupersedesPartsOfDownloadedWhole(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)

	s := seedSupersessionSeries(ctx, t, client, "supersede-basic", "Supersede Basic")

	whole1 := createChapter(ctx, t, client, s, "ch-1", 1, entchapter.StateDownloaded, "")
	const part11File = "[src] Supersede Basic 001.1.cbz"
	part11 := createChapter(ctx, t, client, s, "ch-1.1", 1.1, entchapter.StateDownloaded, part11File)
	part12 := createChapter(ctx, t, client, s, "ch-1.2", 1.2, entchapter.StateWanted, "")

	whole10 := createChapter(ctx, t, client, s, "ch-10", 10, entchapter.StateDownloaded, "")
	side105 := createChapter(ctx, t, client, s, "ch-10.5", 10.5, entchapter.StateDownloaded, "")

	// Write the on-disk CBZ for 1.1 so removal can be observed.
	seriesDir := disk.SeriesDir(storageDir, "Other", "Supersede Basic")
	if err := os.MkdirAll(seriesDir, 0o750); err != nil {
		t.Fatalf("mkdir series dir: %v", err)
	}
	filePath := filepath.Join(seriesDir, part11File)
	if err := os.WriteFile(filePath, []byte("cbz-bytes"), 0o600); err != nil {
		t.Fatalf("write cbz: %v", err)
	}

	d := newSupersessionDispatcher(client, storageDir, true)
	superseded, reverted, err := d.DetectSupersededParts(ctx)
	if err != nil {
		t.Fatalf("DetectSupersededParts: %v", err)
	}
	if superseded != 2 {
		t.Errorf("superseded = %d, want 2", superseded)
	}
	if reverted != 0 {
		t.Errorf("reverted = %d, want 0", reverted)
	}

	assertChapterState(ctx, t, client, part11.ID, entchapter.StateSuperseded, "part 1.1")
	assertChapterState(ctx, t, client, part12.ID, entchapter.StateSuperseded, "part 1.2")
	assertChapterState(ctx, t, client, whole1.ID, entchapter.StateDownloaded, "whole 1 (untouched)")
	assertChapterState(ctx, t, client, whole10.ID, entchapter.StateDownloaded, "whole 10 (untouched)")
	assertChapterState(ctx, t, client, side105.ID, entchapter.StateDownloaded, "lone side chapter 10.5 (never superseded alone)")

	// The superseded part's filename must be cleared and its CBZ removed.
	got := client.Chapter.GetX(ctx, part11.ID)
	if got.Filename != "" {
		t.Errorf("part 1.1 filename = %q, want cleared", got.Filename)
	}
	if _, statErr := os.Stat(filePath); !os.IsNotExist(statErr) {
		t.Errorf("part 1.1 CBZ file still exists at %s (or unexpected stat error: %v)", filePath, statErr)
	}
}

// TestDetectSupersededParts_RevertsWhenWholeGone proves a superseded part
// reverts to wanted once its whole is no longer downloaded.
func TestDetectSupersededParts_RevertsWhenWholeGone(t *testing.T) {
	runRevertScenario(t, "supersede-revert", "Supersede Revert", entchapter.StateWanted, true)
}

// TestDetectSupersededParts_DisabledRevertsAll proves that when the setting is
// disabled every superseded part reverts to wanted and nothing else happens
// (even though the whole is still downloaded).
func TestDetectSupersededParts_DisabledRevertsAll(t *testing.T) {
	runRevertScenario(t, "supersede-disabled", "Supersede Disabled", entchapter.StateDownloaded, false)
}

// TestDetectSupersededParts_CountsButDoesNotSupersedeFailedPart proves the
// subtlest rule: the >=2-parts-under-N count includes parts in ANY state, but
// only wanted/downloaded parts are actually transitioned to superseded. A
// failed (or downloading) part under a downloaded whole is counted toward the
// threshold yet is left untouched.
func TestDetectSupersededParts_CountsButDoesNotSupersedeFailedPart(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)

	s := seedSupersessionSeries(ctx, t, client, "supersede-count-not-touch", "Supersede Count Not Touch")

	whole1 := createChapter(ctx, t, client, s, "ch-1", 1, entchapter.StateDownloaded, "")
	part11Failed := createChapter(ctx, t, client, s, "ch-1.1", 1.1, entchapter.StateFailed, "")
	part12Wanted := createChapter(ctx, t, client, s, "ch-1.2", 1.2, entchapter.StateWanted, "")

	whole2 := createChapter(ctx, t, client, s, "ch-2", 2, entchapter.StateDownloaded, "")
	part21Downloading := createChapter(ctx, t, client, s, "ch-2.1", 2.1, entchapter.StateDownloading, "")
	part22Wanted := createChapter(ctx, t, client, s, "ch-2.2", 2.2, entchapter.StateWanted, "")

	d := newSupersessionDispatcher(client, storageDir, true)
	superseded, reverted, err := d.DetectSupersededParts(ctx)
	if err != nil {
		t.Fatalf("DetectSupersededParts: %v", err)
	}
	if superseded != 2 {
		t.Errorf("superseded = %d, want 2", superseded)
	}
	if reverted != 0 {
		t.Errorf("reverted = %d, want 0", reverted)
	}

	assertChapterState(ctx, t, client, part11Failed.ID, entchapter.StateFailed, "part 1.1 (failed, counted but not superseded)")
	assertChapterState(ctx, t, client, part12Wanted.ID, entchapter.StateSuperseded, "part 1.2")
	assertChapterState(ctx, t, client, whole1.ID, entchapter.StateDownloaded, "whole 1 (untouched)")

	assertChapterState(ctx, t, client, part21Downloading.ID, entchapter.StateDownloading, "part 2.1 (downloading, counted but not superseded)")
	assertChapterState(ctx, t, client, part22Wanted.ID, entchapter.StateSuperseded, "part 2.2")
	assertChapterState(ctx, t, client, whole2.ID, entchapter.StateDownloaded, "whole 2 (untouched)")
}

// TestDetectSupersededParts_MultiSeriesIsolation proves the detector processes
// each series independently in one pass: supersession in series A never
// affects series B and both are handled by a single DetectSupersededParts call.
func TestDetectSupersededParts_MultiSeriesIsolation(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)

	seriesA := seedSupersessionSeries(ctx, t, client, "supersede-multi-a", "Supersede Multi A")
	wholeA := createChapter(ctx, t, client, seriesA, "a-1", 1, entchapter.StateDownloaded, "")
	partA1 := createChapter(ctx, t, client, seriesA, "a-1.1", 1.1, entchapter.StateWanted, "")
	partA2 := createChapter(ctx, t, client, seriesA, "a-1.2", 1.2, entchapter.StateWanted, "")

	seriesB := seedSupersessionSeries(ctx, t, client, "supersede-multi-b", "Supersede Multi B")
	wholeB := createChapter(ctx, t, client, seriesB, "b-5", 5, entchapter.StateDownloaded, "")
	partB1 := createChapter(ctx, t, client, seriesB, "b-5.1", 5.1, entchapter.StateWanted, "")
	partB2 := createChapter(ctx, t, client, seriesB, "b-5.2", 5.2, entchapter.StateWanted, "")

	d := newSupersessionDispatcher(client, storageDir, true)
	superseded, reverted, err := d.DetectSupersededParts(ctx)
	if err != nil {
		t.Fatalf("DetectSupersededParts: %v", err)
	}
	if superseded != 4 {
		t.Errorf("superseded = %d, want 4", superseded)
	}
	if reverted != 0 {
		t.Errorf("reverted = %d, want 0", reverted)
	}

	assertChapterState(ctx, t, client, partA1.ID, entchapter.StateSuperseded, "series A part 1.1")
	assertChapterState(ctx, t, client, partA2.ID, entchapter.StateSuperseded, "series A part 1.2")
	assertChapterState(ctx, t, client, wholeA.ID, entchapter.StateDownloaded, "series A whole 1 (untouched)")

	assertChapterState(ctx, t, client, partB1.ID, entchapter.StateSuperseded, "series B part 5.1")
	assertChapterState(ctx, t, client, partB2.ID, entchapter.StateSuperseded, "series B part 5.2")
	assertChapterState(ctx, t, client, wholeB.ID, entchapter.StateDownloaded, "series B whole 5 (untouched)")
}

// TestDetectSupersededParts_MissingFileTolerated proves that superseding a
// downloaded part whose CBZ is already gone from disk (RemoveChapterFile
// no-ops with removed=false, err=nil) still transitions the part to superseded
// and clears its filename — no error surfaces.
func TestDetectSupersededParts_MissingFileTolerated(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)

	s := seedSupersessionSeries(ctx, t, client, "supersede-missing-file", "Supersede Missing File")

	createChapter(ctx, t, client, s, "ch-1", 1, entchapter.StateDownloaded, "")
	// Both parts carry a filename but the CBZ is never written to disk.
	part11 := createChapter(ctx, t, client, s, "ch-1.1", 1.1, entchapter.StateDownloaded, "[src] Supersede Missing File 001.1.cbz")
	part12 := createChapter(ctx, t, client, s, "ch-1.2", 1.2, entchapter.StateDownloaded, "[src] Supersede Missing File 001.2.cbz")

	d := newSupersessionDispatcher(client, storageDir, true)
	superseded, reverted, err := d.DetectSupersededParts(ctx)
	if err != nil {
		t.Fatalf("DetectSupersededParts: %v", err)
	}
	if superseded != 2 {
		t.Errorf("superseded = %d, want 2", superseded)
	}
	if reverted != 0 {
		t.Errorf("reverted = %d, want 0", reverted)
	}

	assertChapterState(ctx, t, client, part11.ID, entchapter.StateSuperseded, "part 1.1")
	assertChapterState(ctx, t, client, part12.ID, entchapter.StateSuperseded, "part 1.2")

	got11 := client.Chapter.GetX(ctx, part11.ID)
	if got11.Filename != "" {
		t.Errorf("part 1.1 filename = %q, want cleared", got11.Filename)
	}
	got12 := client.Chapter.GetX(ctx, part12.ID)
	if got12.Filename != "" {
		t.Errorf("part 1.2 filename = %q, want cleared", got12.Filename)
	}
}

// TestDetectSupersededParts_Idempotent proves a second run of the supersession
// scenario finds nothing new to supersede.
func TestDetectSupersededParts_Idempotent(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storageDir := mustTempDir(t)

	s := seedSupersessionSeries(ctx, t, client, "supersede-idempotent", "Supersede Idempotent")
	createChapter(ctx, t, client, s, "ch-1", 1, entchapter.StateDownloaded, "")
	createChapter(ctx, t, client, s, "ch-1.1", 1.1, entchapter.StateDownloaded, "")
	createChapter(ctx, t, client, s, "ch-1.2", 1.2, entchapter.StateWanted, "")

	d := newSupersessionDispatcher(client, storageDir, true)
	first, _, err := d.DetectSupersededParts(ctx)
	if err != nil {
		t.Fatalf("DetectSupersededParts (first run): %v", err)
	}
	if first != 2 {
		t.Fatalf("first run superseded = %d, want 2", first)
	}

	second, secondReverted, err := d.DetectSupersededParts(ctx)
	if err != nil {
		t.Fatalf("DetectSupersededParts (second run): %v", err)
	}
	if second != 0 {
		t.Errorf("second run superseded = %d, want 0 (idempotent)", second)
	}
	if secondReverted != 0 {
		t.Errorf("second run reverted = %d, want 0 (whole still downloaded)", secondReverted)
	}
}
