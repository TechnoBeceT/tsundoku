package series_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/series"
)

// fakeProgressPusher is a series.ProgressPusher test double recording every
// call it received, guarded by a mutex + a channel so a test can wait for
// the DETACHED goroutine SetProgress fires without a flaky sleep.
type fakeProgressPusher struct {
	mu      sync.Mutex
	calls   []pushCall
	done    chan struct{}
	callErr error
}

type pushCall struct {
	seriesID uuid.UUID
	furthest float64
}

func newFakeProgressPusher() *fakeProgressPusher {
	return &fakeProgressPusher{done: make(chan struct{}, 8)}
}

func (f *fakeProgressPusher) PushProgress(_ context.Context, seriesID uuid.UUID, localFurthest float64) error {
	f.mu.Lock()
	f.calls = append(f.calls, pushCall{seriesID: seriesID, furthest: localFurthest})
	f.mu.Unlock()
	f.done <- struct{}{}
	return f.callErr
}

func (f *fakeProgressPusher) waitForCall(t *testing.T) {
	t.Helper()
	select {
	case <-f.done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the detached PushProgress call")
	}
}

func (f *fakeProgressPusher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// seedOneChapter creates a minimal series with one chapter (number 3) and
// returns both ids — the fixture every test in this file shares.
func seedOneChapter(ctx context.Context, t *testing.T, client *ent.Client, title, slug string) (seriesID, chapterID uuid.UUID) {
	t.Helper()
	row := client.Series.Create().
		SetTitle(title).
		SetSlug(slug).
		SetCategoryID(catID(ctx, client, "Manga")).
		SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeriesID(row.ID).
		SetChapterKey("c-3").
		SetNumber(3).
		SetState(entchapter.StateDownloaded).
		SaveX(ctx)
	return row.ID, ch.ID
}

// TestSetProgress_FiresProgressPusherOnMarkRead confirms marking a chapter
// read fires the attached ProgressPusher, detached, with the series id and
// the chapter's own number.
func TestSetProgress_FiresProgressPusherOnMarkRead(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	seriesID, chapterID := seedOneChapter(ctx, t, client, "Pusher Series", "pusher-series")

	pusher := newFakeProgressPusher()
	svc := series.NewService(client, t.TempDir(), 14).WithProgressPusher(pusher)

	if _, err := svc.SetProgress(ctx, chapterID, 0, true); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}

	pusher.waitForCall(t)
	if pusher.callCount() != 1 {
		t.Fatalf("PushProgress calls = %d, want 1", pusher.callCount())
	}
	pusher.mu.Lock()
	got := pusher.calls[0]
	pusher.mu.Unlock()
	if got.seriesID != seriesID || got.furthest != 3 {
		t.Fatalf("PushProgress call = %+v, want seriesID=%v furthest=3", got, seriesID)
	}
}

// TestSetProgress_UnreadDoesNotFireProgressPusher confirms un-marking a
// chapter (read=false) never fires the hook — there is no "progress" to
// push when a chapter is being marked NOT read.
func TestSetProgress_UnreadDoesNotFireProgressPusher(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	_, chapterID := seedOneChapter(ctx, t, client, "Unread Series", "unread-series")

	pusher := newFakeProgressPusher()
	svc := series.NewService(client, t.TempDir(), 14).WithProgressPusher(pusher)

	if _, err := svc.SetProgress(ctx, chapterID, 0, false); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}

	select {
	case <-pusher.done:
		t.Fatal("PushProgress fired for read=false, want no call")
	case <-time.After(200 * time.Millisecond):
		// expected: no call within the wait window.
	}
}

// TestSetProgress_NilProgressPusherIsSafe confirms a Service with no
// ProgressPusher attached (every pre-existing series/reader test's shape)
// marks a chapter read without panicking or blocking — the default,
// untouched behaviour.
func TestSetProgress_NilProgressPusherIsSafe(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	_, chapterID := seedOneChapter(ctx, t, client, "No Pusher Series", "no-pusher-series")

	svc := series.NewService(client, t.TempDir(), 14) // no WithProgressPusher

	got, err := svc.SetProgress(ctx, chapterID, 2, true)
	if err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
	if !got.Read || got.LastReadPage != 2 {
		t.Fatalf("SetProgress result = %+v", got)
	}
}
