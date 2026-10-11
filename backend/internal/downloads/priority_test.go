package downloads_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/ent"
	"sync"
	"testing"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
)

func TestPrioritySnapshotIsFixedAndCancelable(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Priority").SetSlug("priority").SaveX(ctx)
	queued := client.Chapter.Create().SetSeries(s).SetChapterKey("wanted").SaveX(ctx)
	client.Chapter.Create().SetSeries(s).SetChapterKey("upgrade").SetState(chapter.StateUpgradeAvailable).SaveX(ctx)
	for _, state := range []chapter.State{chapter.StateDownloaded, chapter.StateDownloading, chapter.StateUpgrading, chapter.StatePermanentlyFailed, chapter.StateIgnored, chapter.StateSuperseded} {
		client.Chapter.Create().SetSeries(s).SetChapterKey(string(state)).SetState(state).SaveX(ctx)
	}
	svc := downloads.NewService(client)
	status, err := svc.SetPriority(ctx, s.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if status.Remaining != 2 {
		t.Fatalf("snapshot remaining %d, want2", status.Remaining)
	}
	late := client.Chapter.Create().SetSeries(s).SetChapterKey("late").SaveX(ctx)
	repeatPriorityConcurrently(t, ctx, client, s.ID)
	if client.Chapter.GetX(ctx, late.ID).DownloadPriority {
		t.Fatal("repeated action extended snapshot")
	}
	if !client.Chapter.GetX(ctx, queued.ID).DownloadPriority {
		t.Fatal("reconstructed service lost snapshot")
	}
	status, err = svc.SetPriority(ctx, s.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Remaining != 0 || client.Chapter.Query().Where(chapter.DownloadPriority(true)).CountX(ctx) != 0 {
		t.Fatal("cancel retained priority")
	}
}

func repeatPriorityConcurrently(t *testing.T, ctx context.Context, client *ent.Client, id uuid.UUID) {
	t.Helper()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := downloads.NewService(client).SetPriority(ctx, id, true); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
