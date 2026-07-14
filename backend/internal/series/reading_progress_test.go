package series_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// seedReadingProgressSeries creates a series with a mix of chapters — some
// already read (with a distinct pre-set readAt so the test can prove it
// survives untouched), some unread, one with a nil number (unparseable —
// must be left alone by SetReadingProgress) — so the reset assertions are
// non-vacuous in both directions.
func seedReadingProgressSeries(ctx context.Context, t *testing.T, client *ent.Client) uuid.UUID {
	t.Helper()

	s := client.Series.Create().
		SetTitle("Reset Me").SetSlug("reset-me").
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)

	// Chapter 1: already read, with an OLD readAt that must survive a reset
	// to a target >= 1 untouched (re-confirming must not re-stamp it).
	oldReadAt := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("ch-1").SetNumber(1.0).
		SetState("downloaded").SetRead(true).SetReadAt(oldReadAt).SetLastReadPage(19).
		SaveX(ctx)
	// Chapter 2: unread.
	client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("ch-2").SetNumber(2.0).
		SetState("downloaded").SaveX(ctx)
	// Chapter 3: unread.
	client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("ch-3").SetNumber(3.0).
		SetState("wanted").SaveX(ctx)
	// Chapter 5: already read + has a last_read_page, must be reset to
	// unread/page-0/no-readAt when the target regresses below it.
	client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("ch-5").SetNumber(5.0).
		SetState("downloaded").SetRead(true).SetReadAt(time.Now().UTC()).SetLastReadPage(7).
		SaveX(ctx)
	// A chapter with no parsed number — must never be touched either direction.
	client.Chapter.Create().
		SetSeriesID(s.ID).SetChapterKey("ch-unparsed").
		SetState("downloaded").SaveX(ctx)

	return s.ID
}

func chapterByKey(ctx context.Context, client *ent.Client, key string) *ent.Chapter {
	return client.Chapter.Query().Where(entchapter.ChapterKey(key)).OnlyX(ctx)
}

// TestSetReadingProgress_MarksReadUpToTargetAndUnreadPastIt is the core
// QCAT-242 semantics proof: number <= target read (already-read chapters
// keep their original readAt), number > target unread with page/readAt
// cleared, and a nil-number chapter is left entirely untouched.
func TestSetReadingProgress_MarksReadUpToTargetAndUnreadPastIt(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := series.NewService(client, t.TempDir(), 14)
	seriesID := seedReadingProgressSeries(ctx, t, client)

	affected, err := svc.SetReadingProgress(ctx, seriesID, 2)
	if err != nil {
		t.Fatalf("SetReadingProgress: %v", err)
	}
	// ch-2 (unread->read) transitions, and BOTH ch-3 and ch-5 are matched by
	// the unconditional "past target" reset (ch-3 was already unread but is
	// still matched — see SetReadingProgress's own doc comment on why that
	// statement has no Read(true) filter); ch-1 (already read, stays read)
	// and ch-unparsed (no number) are matched by neither statement.
	if affected != 3 {
		t.Fatalf("affected = %d, want 3", affected)
	}

	assertAlreadyReadKeepsOriginalReadAt(ctx, t, client, "ch-1")
	assertNewlyRead(ctx, t, client, "ch-2")
	assertUnread(ctx, t, client, "ch-3")
	assertUnread(ctx, t, client, "ch-5")
	assertUntouched(ctx, t, client, "ch-unparsed")
}

// assertAlreadyReadKeepsOriginalReadAt fails t unless key's chapter is read
// with its ORIGINAL pre-set readAt (2020-01-01, seeded by
// seedReadingProgressSeries) — re-confirming an already-read chapter must
// never re-stamp it.
func assertAlreadyReadKeepsOriginalReadAt(ctx context.Context, t *testing.T, client *ent.Client, key string) {
	t.Helper()
	ch := chapterByKey(ctx, client, key)
	want := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if !ch.Read || ch.ReadAt == nil || !ch.ReadAt.Equal(want) {
		t.Fatalf("%s (already read, <= target): want read=true with ORIGINAL readAt preserved, got read=%v readAt=%v", key, ch.Read, ch.ReadAt)
	}
}

// assertNewlyRead fails t unless key's chapter is read with a freshly
// stamped (non-nil) readAt.
func assertNewlyRead(ctx context.Context, t *testing.T, client *ent.Client, key string) {
	t.Helper()
	ch := chapterByKey(ctx, client, key)
	if !ch.Read || ch.ReadAt == nil {
		t.Fatalf("%s (newly <= target): want read=true with a stamped readAt, got read=%v readAt=%v", key, ch.Read, ch.ReadAt)
	}
}

// assertUnread fails t unless key's chapter is fully unread: read=false,
// readAt=nil, lastReadPage=0.
func assertUnread(ctx context.Context, t *testing.T, client *ent.Client, key string) {
	t.Helper()
	ch := chapterByKey(ctx, client, key)
	if ch.Read || ch.ReadAt != nil || ch.LastReadPage != 0 {
		t.Fatalf("%s (> target): want read=false readAt=nil lastReadPage=0, got read=%v readAt=%v lastReadPage=%d", key, ch.Read, ch.ReadAt, ch.LastReadPage)
	}
}

// assertUntouched fails t unless key's chapter carries neither a read flag
// nor a readAt — the shape a nil-number chapter must keep regardless of
// target (SetReadingProgress has nothing to compare its number against).
func assertUntouched(ctx context.Context, t *testing.T, client *ent.Client, key string) {
	t.Helper()
	ch := chapterByKey(ctx, client, key)
	if ch.Read || ch.ReadAt != nil {
		t.Fatalf("%s (nil number): must be left untouched, got read=%v readAt=%v", key, ch.Read, ch.ReadAt)
	}
}

// TestSetReadingProgress_ZeroMarksEverythingUnread confirms target=0 is
// "re-read from scratch" — every numbered chapter (including an already-read
// one) ends up unread.
func TestSetReadingProgress_ZeroMarksEverythingUnread(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := series.NewService(client, t.TempDir(), 14)
	seriesID := seedReadingProgressSeries(ctx, t, client)

	if _, err := svc.SetReadingProgress(ctx, seriesID, 0); err != nil {
		t.Fatalf("SetReadingProgress: %v", err)
	}

	for _, key := range []string{"ch-1", "ch-2", "ch-3", "ch-5"} {
		ch := chapterByKey(ctx, client, key)
		if ch.Read || ch.ReadAt != nil || ch.LastReadPage != 0 {
			t.Fatalf("%s: want fully unread after target=0, got read=%v readAt=%v lastReadPage=%d", key, ch.Read, ch.ReadAt, ch.LastReadPage)
		}
	}
}

// TestSetReadingProgress_UnknownSeries confirms a bogus seriesID fails
// closed with ErrSeriesNotFound rather than silently affecting 0 rows.
func TestSetReadingProgress_UnknownSeries(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	svc := series.NewService(client, t.TempDir(), 14)

	_, err := svc.SetReadingProgress(ctx, uuid.New(), 5)
	if err != series.ErrSeriesNotFound {
		t.Fatalf("SetReadingProgress: err = %v, want series.ErrSeriesNotFound", err)
	}
}
