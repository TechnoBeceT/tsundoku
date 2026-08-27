package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

func TestRunOnceAt_CancelledAfterAdmissionGrantRollsBackClaim(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := t.TempDir()
	s := client.Series.Create().SetTitle("Download grant race").SetSlug("download-grant-race").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("source").SetImportance(1).SaveX(ctx)
	links := admissionPageLinks()
	pc := client.ProviderChapter.Create().
		SetSeriesProvider(sp).
		SetChapterKey("1").
		SetURL("https://example.test/1").
		SetProviderIndex(0).
		SetAttempts(1).
		SetLastError("prior source error").
		SetPageLinks(links).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetLastError("prior chapter error").SaveX(ctx)
	marker := seedAdmissionStaging(t, stagingRoot, pc.ID)

	cctx, cancel := context.WithCancel(ctx)
	installPostStateWriteHook(client, entchapter.StateDownloading, cancel)
	policy := admissionPolicy()
	f := &admissionFetcher{}
	d := New(client, f, sse.NewHub(), Config{StagingRoot: stagingRoot}, policy, sourcegate.NewService(client, policy))
	global := semaphore.NewWeighted(1)

	dispatched, err := d.RunOnceAt(cctx, time.Now(), map[string]int{}, global)
	if err != nil {
		t.Fatalf("RunOnceAt: %v", err)
	}
	if dispatched != 0 {
		t.Fatalf("dispatched = %d, want 0 after the admitted claim rolled back", dispatched)
	}
	assertAdmissionStateUnchanged(t, client, ch.ID, entchapter.StateWanted, pc.ID, links, marker)
	assertAdmissionReleased(t, global, f)
}

func TestUpgradeAll_CancelledAfterAdmissionGrantRollsBackClaim(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := t.TempDir()
	s := client.Series.Create().SetTitle("Upgrade grant race").SetSlug("upgrade-grant-race").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("source").SetImportance(2).SaveX(ctx)
	links := admissionPageLinks()
	pc := client.ProviderChapter.Create().
		SetSeriesProvider(sp).
		SetChapterKey("1").
		SetURL("https://example.test/1").
		SetProviderIndex(0).
		SetAttempts(1).
		SetLastError("prior source error").
		SetPageLinks(links).
		SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetState(entchapter.StateUpgradeAvailable).
		SetLastError("prior chapter error").
		SaveX(ctx)
	marker := seedAdmissionStaging(t, stagingRoot, pc.ID)

	cctx, cancel := context.WithCancel(ctx)
	installPostStateWriteHook(client, entchapter.StateUpgrading, cancel)
	policy := admissionPolicy()
	f := &admissionFetcher{}
	d := New(client, f, sse.NewHub(), Config{StagingRoot: stagingRoot}, policy, sourcegate.NewService(client, policy))
	global := semaphore.NewWeighted(1)

	upgraded, err := d.UpgradeAll(cctx, nil, global)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("UpgradeAll error = %v, want context canceled", err)
	}
	if upgraded != 0 {
		t.Fatalf("upgraded = %d, want 0 after the admitted claim rolled back", upgraded)
	}
	assertAdmissionStateUnchanged(t, client, ch.ID, entchapter.StateUpgradeAvailable, pc.ID, links, marker)
	assertAdmissionReleased(t, global, f)
}

func TestRunOnceAt_AdmissionStateWriteFailureIsNotSourceFailure(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := t.TempDir()
	s := client.Series.Create().SetTitle("Download admission write").SetSlug("download-admission-write").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("source").SetImportance(1).SaveX(ctx)
	links := admissionPageLinks()
	pc := client.ProviderChapter.Create().
		SetSeriesProvider(sp).
		SetChapterKey("1").
		SetURL("https://example.test/1").
		SetProviderIndex(0).
		SetAttempts(1).
		SetLastError("prior source error").
		SetPageLinks(links).
		SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetLastError("prior chapter error").SaveX(ctx)
	marker := seedAdmissionStaging(t, stagingRoot, pc.ID)
	installStateWriteFailure(client, entchapter.StateDownloading)
	policy := admissionPolicy()
	f := &admissionFetcher{}
	d := New(client, f, sse.NewHub(), Config{StagingRoot: stagingRoot}, policy, sourcegate.NewService(client, policy))
	global := semaphore.NewWeighted(1)

	dispatched, err := d.RunOnceAt(ctx, time.Now(), map[string]int{}, global)
	if err != nil {
		t.Fatalf("RunOnceAt: %v", err)
	}
	if dispatched != 0 {
		t.Fatalf("dispatched = %d, want 0 when the admission state write fails", dispatched)
	}
	assertAdmissionStateUnchanged(t, client, ch.ID, entchapter.StateWanted, pc.ID, links, marker)
	assertAdmissionReleased(t, global, f)
}

func TestUpgradeAll_AdmissionStateWriteFailureIsHardLocalError(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	stagingRoot := t.TempDir()
	s := client.Series.Create().SetTitle("Upgrade admission write").SetSlug("upgrade-admission-write").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("source").SetImportance(2).SaveX(ctx)
	links := admissionPageLinks()
	pc := client.ProviderChapter.Create().
		SetSeriesProvider(sp).
		SetChapterKey("1").
		SetURL("https://example.test/1").
		SetProviderIndex(0).
		SetAttempts(1).
		SetLastError("prior source error").
		SetPageLinks(links).
		SaveX(ctx)
	ch := client.Chapter.Create().
		SetSeries(s).
		SetChapterKey("1").
		SetState(entchapter.StateUpgradeAvailable).
		SetLastError("prior chapter error").
		SaveX(ctx)
	marker := seedAdmissionStaging(t, stagingRoot, pc.ID)
	installStateWriteFailure(client, entchapter.StateUpgrading)
	policy := admissionPolicy()
	f := &admissionFetcher{}
	d := New(client, f, sse.NewHub(), Config{StagingRoot: stagingRoot}, policy, sourcegate.NewService(client, policy))
	global := semaphore.NewWeighted(1)

	upgraded, err := d.UpgradeAll(ctx, nil, global)
	if err == nil || !errors.Is(err, errAdmissionStateWrite) {
		t.Fatalf("UpgradeAll error = %v, want injected admission state-write error", err)
	}
	if upgraded != 0 {
		t.Fatalf("upgraded = %d, want 0 when the admission state write fails", upgraded)
	}
	assertAdmissionStateUnchanged(t, client, ch.ID, entchapter.StateUpgradeAvailable, pc.ID, links, marker)
	assertAdmissionReleased(t, global, f)
}

var errAdmissionStateWrite = errors.New("injected admission state-write failure")

func admissionPageLinks() []fetcher.PageLink {
	return []fetcher.PageLink{{URL: "https://example.test/page", ImageURL: "https://example.test/image"}}
}

func admissionPolicy() settings.Static {
	return settings.Static{
		Retries:              3,
		DownloadConc:         1,
		SourcesFailureThresh: 1,
		SourcesCooldownIv:    time.Hour,
	}
}

func installPostStateWriteHook(client *ent.Client, target entchapter.State, after func()) {
	var once sync.Once
	client.Chapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			if err == nil && mutationSetsChapterState(mutation, target) {
				once.Do(after)
			}
			return value, err
		})
	})
}

func installStateWriteFailure(client *ent.Client, target entchapter.State) {
	client.Chapter.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			if mutationSetsChapterState(mutation, target) {
				return nil, errAdmissionStateWrite
			}
			return next.Mutate(ctx, mutation)
		})
	})
}

func mutationSetsChapterState(mutation ent.Mutation, target entchapter.State) bool {
	chapterMutation, ok := mutation.(*ent.ChapterMutation)
	if !ok {
		return false
	}
	state, exists := chapterMutation.State()
	return exists && state == target
}

func seedAdmissionStaging(t *testing.T, root string, providerChapterID uuid.UUID) string {
	t.Helper()
	dir := filepath.Join(root, providerChapterID.String())
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create staging fixture: %v", err)
	}
	marker := filepath.Join(dir, "keep")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatalf("write staging fixture: %v", err)
	}
	return marker
}

func assertAdmissionStateUnchanged(
	t *testing.T,
	client *ent.Client,
	chapterID uuid.UUID,
	wantState entchapter.State,
	providerChapterID uuid.UUID,
	wantLinks []fetcher.PageLink,
	stagingMarker string,
) {
	t.Helper()
	ctx := context.Background()
	ch := client.Chapter.GetX(ctx, chapterID)
	if ch.State != wantState || ch.LastError != "prior chapter error" {
		t.Fatalf("chapter state/error = %s/%q, want %s/%q", ch.State, ch.LastError, wantState, "prior chapter error")
	}
	pc := client.ProviderChapter.GetX(ctx, providerChapterID)
	if pc.Attempts != 1 || pc.LastError != "prior source error" || pc.NextAttemptAt != nil || !reflect.DeepEqual(pc.PageLinks, wantLinks) {
		t.Fatalf("provider state changed: attempts=%d last_error=%q next_attempt_at=%v page_links=%v", pc.Attempts, pc.LastError, pc.NextAttemptAt, pc.PageLinks)
	}
	if info, err := os.Stat(stagingMarker); err != nil || info.Size() != int64(len("unchanged")) {
		t.Fatalf("staging marker changed: info=%v err=%v", info, err)
	}
	if got := client.SourceCircuitState.Query().CountX(ctx); got != 0 {
		t.Fatalf("source breaker rows = %d, want 0", got)
	}
}

func assertAdmissionReleased(t *testing.T, global *semaphore.Weighted, f *admissionFetcher) {
	t.Helper()
	if !global.TryAcquire(1) {
		t.Fatal("global admission permit was not released")
	}
	global.Release(1)
	if got := f.calls.Load(); got != 0 {
		t.Fatalf("engine calls = %d, want 0", got)
	}
}
