package syncsvc_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/tracker"
)

// seedChapter creates a downloaded, syncable Chapter row numbered n under
// seriesID with a unique chapter_key (so a series can carry many rows for
// these tests without colliding on the (series_id, chapter_key) unique
// index).
func seedChapter(ctx context.Context, t *testing.T, client *ent.Client, seriesID uuid.UUID, key string, n float64) *ent.Chapter {
	t.Helper()
	ch, err := client.Chapter.Create().
		SetSeriesID(seriesID).
		SetChapterKey(key).
		SetNumber(n).
		SetState(entchapter.StateDownloaded).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed chapter %s (number=%v): %v", key, n, err)
	}
	return ch
}

// markChapterRead directly flags chapterID read=true with a fixed readAt —
// used to seed a chapter that was ALREADY read before SyncNow runs, so a
// test can prove its read_at is never rewritten (idempotence).
func markChapterRead(ctx context.Context, t *testing.T, client *ent.Client, chapterID uuid.UUID, readAt time.Time) {
	t.Helper()
	if _, err := client.Chapter.UpdateOneID(chapterID).SetRead(true).SetReadAt(readAt).Save(ctx); err != nil {
		t.Fatalf("mark chapter %s read: %v", chapterID, err)
	}
}

// reloadChapter re-reads chapterID's current row.
func reloadChapter(ctx context.Context, t *testing.T, client *ent.Client, chapterID uuid.UUID) *ent.Chapter {
	t.Helper()
	ch, err := client.Chapter.Query().Where(entchapter.IDEQ(chapterID)).Only(ctx)
	if err != nil {
		t.Fatalf("reload chapter %s: %v", chapterID, err)
	}
	return ch
}

// TestSyncNow_MarkLocalRead_RemoteAheadMarksChapters proves the pull-
// direction convergence itself (spec §2b): local reads to 50, the tracker
// reports 60, so chapters 1..60 are marked read locally and 61+ stay
// untouched. A chapter that was ALREADY read before the sync (chapter 10,
// stamped with a fixed pre-sync readAt) keeps its ORIGINAL read_at — proof
// the pull direction never rewrites an already-read chapter (idempotence at
// the field level, not just the boolean).
func TestSyncNow_MarkLocalRead_RemoteAheadMarksChapters(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Remote Ahead Mark-Read", "remote-ahead-mark-read")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 50, 0)

	chapters := make([]*ent.Chapter, 0, 70)
	for i := 1; i <= 70; i++ {
		chapters = append(chapters, seedChapter(ctx, t, client, seriesID, chKey(i), float64(i)))
	}
	preSyncReadAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	markChapterRead(ctx, t, client, chapters[9].ID, preSyncReadAt) // chapter 10

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 60}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	for i, ch := range chapters {
		n := i + 1
		fresh := reloadChapter(ctx, t, client, ch.ID)
		wantRead := n <= 60
		if fresh.Read != wantRead {
			t.Fatalf("chapter %d read = %v, want %v", n, fresh.Read, wantRead)
		}
		if n == 10 {
			if fresh.ReadAt == nil || !fresh.ReadAt.Equal(preSyncReadAt) {
				t.Fatalf("chapter 10 read_at = %v, want unchanged original %v", fresh.ReadAt, preSyncReadAt)
			}
		}
		if n > 60 && fresh.ReadAt != nil {
			t.Fatalf("chapter %d read_at = %v, want nil (never marked)", n, fresh.ReadAt)
		}
	}
}

// TestSyncNow_MarkLocalRead_LocalAheadNoSpuriousMarks proves the local-
// ahead / equal case: Converge picks the LOCAL value (60) as the target, so
// mark-read marks up through 60 (matching what local already claims) and
// never marks anything past the converged target, even though the remote
// side (50) is lower.
func TestSyncNow_MarkLocalRead_LocalAheadNoSpuriousMarks(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Local Ahead Mark-Read", "local-ahead-mark-read")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 60, 0)

	chapters := make([]*ent.Chapter, 0, 70)
	for i := 1; i <= 70; i++ {
		chapters = append(chapters, seedChapter(ctx, t, client, seriesID, chKey(i), float64(i)))
	}

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 50}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	for i, ch := range chapters {
		n := i + 1
		fresh := reloadChapter(ctx, t, client, ch.ID)
		wantRead := n <= 60
		if fresh.Read != wantRead {
			t.Fatalf("chapter %d read = %v, want %v (converged=60, never past it)", n, fresh.Read, wantRead)
		}
	}
}

// TestSyncNow_MarkLocalRead_CorruptionStopsWalk proves numbering corruption
// (a duplicate chapter number, e.g. a "Vol 2 Ch 1" reusing an earlier
// chapter's number) stops kernel.MarkReadUpTo's walk permanently — a
// chapter numerically <= the converged target that sorts AFTER the
// corruption point is never marked read, even though a naive "count every
// number <= target" implementation would mark it.
func TestSyncNow_MarkLocalRead_CorruptionStopsWalk(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Corruption Mark-Read", "corruption-mark-read")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 0, 0)

	ch1 := seedChapter(ctx, t, client, seriesID, "c1", 1)
	ch2a := seedChapter(ctx, t, client, seriesID, "c2a", 2)
	ch2b := seedChapter(ctx, t, client, seriesID, "c2b-duplicate", 2) // numbering corruption: reuses 2
	ch3 := seedChapter(ctx, t, client, seriesID, "c3", 3)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 10}, nil // far beyond every seeded number
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	// Chapter 1 always counts; chapter 3 (sorting after the duplicate "2")
	// must NEVER be marked, even though 3 <= remote's 10 — the walk stopped
	// at the duplicate before reaching it.
	if !reloadChapter(ctx, t, client, ch1.ID).Read {
		t.Fatalf("chapter 1 (number=1) not marked read, want read")
	}
	if reloadChapter(ctx, t, client, ch3.ID).Read {
		t.Fatalf("chapter 3 (number=3, sorts after the duplicate) marked read, want untouched — corruption must stop the walk")
	}
	// Exactly ONE of the two number=2 rows is counted (Postgres tie order
	// between equal numbers is unspecified) — total read count across all
	// four seeded chapters must be exactly 2, never 3 or 4.
	readCount := 0
	for _, ch := range []*ent.Chapter{ch1, ch2a, ch2b, ch3} {
		if reloadChapter(ctx, t, client, ch.ID).Read {
			readCount++
		}
	}
	if readCount != 2 {
		t.Fatalf("total marked-read chapters = %d, want exactly 2 (1 plus one of the tied number=2 rows)", readCount)
	}
}

// TestSyncNow_MarkLocalRead_UnparseableChapterPreservesPairing is the
// off-by-one guard: an unparseable chapter (number == -1, the chapter
// normaliser's sentinel) sorts FIRST in ascending order but must be
// filtered by kernel.SyncableNumbers BEFORE the syncable numbers are fed to
// kernel.MarkReadUpTo. A naive implementation that computed readCount
// against the FILTERED numbers but sliced the UNFILTERED row list would
// wrongly mark the unparseable chapter read (index 0) instead of chapter
// number 1 (the true index 0 of the filtered pairing).
func TestSyncNow_MarkLocalRead_UnparseableChapterPreservesPairing(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Unparseable Mark-Read", "unparseable-mark-read")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 0, 0)

	unparseable := seedChapter(ctx, t, client, seriesID, "unparseable", -1)
	ch1 := seedChapter(ctx, t, client, seriesID, "c1", 1)
	ch2 := seedChapter(ctx, t, client, seriesID, "c2", 2)
	ch3 := seedChapter(ctx, t, client, seriesID, "c3", 3)

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 3}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	if reloadChapter(ctx, t, client, unparseable.ID).Read {
		t.Fatalf("unparseable chapter (number=-1) marked read, want untouched — it must be filtered, never counted")
	}
	for n, ch := range map[int]*ent.Chapter{1: ch1, 2: ch2, 3: ch3} {
		if !reloadChapter(ctx, t, client, ch.ID).Read {
			t.Fatalf("chapter %d not marked read, want read (pairing must skip the filtered -1 row without an off-by-one)", n)
		}
	}
}

// TestSyncNow_MarkLocalRead_Idempotent proves a second SyncNow run marks
// nothing new: every already-read chapter's read_at is byte-identical
// across both runs, and no chapter past the converged target becomes read
// on the repeat.
func TestSyncNow_MarkLocalRead_Idempotent(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "Idempotent Mark-Read", "idempotent-mark-read")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 0, 0)

	chapters := make([]*ent.Chapter, 0, 10)
	for i := 1; i <= 10; i++ {
		chapters = append(chapters, seedChapter(ctx, t, client, seriesID, chKey(i), float64(i)))
	}

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 5}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("first SyncNow: %v", err)
	}
	firstPass := make(map[uuid.UUID]*ent.Chapter, len(chapters))
	for _, ch := range chapters {
		firstPass[ch.ID] = reloadChapter(ctx, t, client, ch.ID)
	}

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("second SyncNow: %v", err)
	}
	for _, ch := range chapters {
		assertChapterUnchangedAcrossRepeat(t, firstPass[ch.ID], reloadChapter(ctx, t, client, ch.ID))
	}
}

// assertChapterUnchangedAcrossRepeat fails the test unless second's read
// flag and read_at exactly match first's — the idempotence proof for
// TestSyncNow_MarkLocalRead_Idempotent, extracted to keep that test's own
// cyclomatic complexity within budget.
func assertChapterUnchangedAcrossRepeat(t *testing.T, first, second *ent.Chapter) {
	t.Helper()
	if second.Read != first.Read {
		t.Fatalf("chapter %s read flipped across the repeat run: first=%v second=%v", first.ID, first.Read, second.Read)
	}
	if !first.Read {
		return
	}
	if second.ReadAt == nil || first.ReadAt == nil || !second.ReadAt.Equal(*first.ReadAt) {
		t.Fatalf("chapter %s read_at changed on the repeat run: first=%v second=%v (idempotence broken)", first.ID, first.ReadAt, second.ReadAt)
	}
}

// TestSyncNow_MarkLocalRead_NoExtraPushFromMarkRead proves mark-read never
// triggers an outbound push: with the remote already ahead (Converge picks
// the remote's own value, so pushBack's NextPush correctly declines), the
// local mark-read step marks a batch of chapters read but UpdateEntry is
// called exactly ZERO times. If mark-read routed through series.
// SetProgress (forbidden — see markread.go's doc comment) instead of
// writing ent directly, SetProgress's reading-triggered hook would fire
// PushProgress and this count would be nonzero: a push↔pull loop. This is
// the functional half of the "no loop" proof; the structural half is that
// internal/tracker/syncsvc imports no series package symbol at all and
// markLocalRead calls only ent.Chapter.UpdateOneID (see markread.go).
func TestSyncNow_MarkLocalRead_NoExtraPushFromMarkRead(t *testing.T) {
	ctx := context.Background()
	client := newTestDB(t)
	seriesID := seedSeries(ctx, t, client, "No Loop Mark-Read", "no-loop-mark-read")
	seedConnection(ctx, t, client, fakeTrackerID, "acct-token")
	seedBinding(ctx, t, client, seriesID, fakeTrackerID, "r1", 10, 0)

	for i := 1; i <= 60; i++ {
		seedChapter(ctx, t, client, seriesID, chKey(i), float64(i))
	}

	ft := &fakeTracker{
		id: fakeTrackerID,
		getEntryFn: func(_ context.Context, _, remoteID string) (*tracker.TrackEntry, error) {
			return &tracker.TrackEntry{RemoteID: remoteID, Progress: 60}, nil
		},
	}
	svc := newService(client, ft, nil, nil)

	if _, err := svc.SyncNow(ctx, seriesID); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	if ft.updateEntryCalls != 0 {
		t.Fatalf("UpdateEntry calls = %d, want 0 (remote already led; mark-read must never itself trigger a push)", ft.updateEntryCalls)
	}

	readCount := 0
	rows, err := client.Chapter.Query().Where(entchapter.SeriesID(seriesID)).All(ctx)
	if err != nil {
		t.Fatalf("reload chapters: %v", err)
	}
	for _, ch := range rows {
		if ch.Read {
			readCount++
		}
	}
	if readCount != 60 {
		t.Fatalf("read chapter count = %d, want 60 (mark-read still ran, it just never pushed)", readCount)
	}
}

// chKey builds a deterministic, distinct chapter_key for a numbered test
// chapter.
func chKey(n int) string {
	return "ch-" + strconv.Itoa(n)
}
