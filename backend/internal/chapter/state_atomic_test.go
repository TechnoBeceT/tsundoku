package chapter_test

import (
	"context"
	"testing"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/database/testdb"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

func TestTransitionIfCurrent_ReturnsDatabaseOwnership(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Atomic transition").SetSlug("atomic-transition").SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetLastError("frozen").
		SaveX(ctx)

	claimed, err := chapter.TransitionIfCurrent(
		ctx,
		client,
		ch.ID,
		entchapter.StateWanted,
		entchapter.StateDownloading,
		entchapter.LastErrorEQ("changed"),
	)
	if err != nil {
		t.Fatalf("guarded transition: %v", err)
	}
	if claimed {
		t.Fatal("guarded transition claimed a row whose frozen value did not match")
	}
	if got := client.Chapter.GetX(ctx, ch.ID).State; got != entchapter.StateWanted {
		t.Fatalf("state after failed guard = %s, want wanted", got)
	}

	claimed, err = chapter.TransitionIfCurrent(
		ctx,
		client,
		ch.ID,
		entchapter.StateWanted,
		entchapter.StateDownloading,
		entchapter.LastErrorEQ("frozen"),
	)
	if err != nil {
		t.Fatalf("winning transition: %v", err)
	}
	if !claimed {
		t.Fatal("matching transition did not claim its row")
	}

	claimed, err = chapter.TransitionIfCurrent(ctx, client, ch.ID, entchapter.StateWanted, entchapter.StateDownloading)
	if err != nil {
		t.Fatalf("losing transition: %v", err)
	}
	if claimed {
		t.Fatal("second transition claimed a row already owned by the first")
	}
}

func TestTransitionIfCurrent_RejectsIllegalEdge(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Illegal atomic transition").SetSlug("illegal-atomic-transition").SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SaveX(ctx)

	claimed, err := chapter.TransitionIfCurrent(ctx, client, ch.ID, entchapter.StateWanted, entchapter.StateDownloaded)
	if err == nil {
		t.Fatal("illegal wanted→downloaded transition returned nil error")
	}
	if claimed {
		t.Fatal("illegal wanted→downloaded transition claimed the row")
	}
	if got := client.Chapter.GetX(ctx, ch.ID).State; got != entchapter.StateWanted {
		t.Fatalf("state after illegal transition = %s, want wanted", got)
	}
}

func TestSetState_CompletionTransitionsRemainLegal(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Atomic completions").SetSlug("atomic-completions").SaveX(ctx)
	download := client.Chapter.Create().
		SetSeries(s).SetChapterKey("download").SetState(entchapter.StateDownloading).SaveX(ctx)
	upgrade := client.Chapter.Create().
		SetSeries(s).SetChapterKey("upgrade").SetState(entchapter.StateUpgrading).SaveX(ctx)

	if err := chapter.SetState(ctx, client, download.ID, entchapter.StateDownloaded); err != nil {
		t.Fatalf("downloading→downloaded: %v", err)
	}
	if err := chapter.SetState(ctx, client, upgrade.ID, entchapter.StateDownloaded); err != nil {
		t.Fatalf("upgrading→downloaded: %v", err)
	}
	if got := client.Chapter.GetX(ctx, download.ID).State; got != entchapter.StateDownloaded {
		t.Fatalf("download completion state = %s, want downloaded", got)
	}
	if got := client.Chapter.GetX(ctx, upgrade.ID).State; got != entchapter.StateDownloaded {
		t.Fatalf("upgrade completion state = %s, want downloaded", got)
	}
}
