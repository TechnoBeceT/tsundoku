package download_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/downloads"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	"github.com/technobecet/tsundoku/internal/sse"
)

func TestPrioritySuccessfulDownloadClearsSnapshot(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Priority download").SetSlug("priority-download").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetImportance(10).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetNumber(1).SetDownloadPriority(true).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://example.test/1").SaveX(ctx)
	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 2, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	if _, err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != chapter.StateDownloaded || got.DownloadPriority {
		t.Fatalf("successful download retained snapshot: %s priority=%t", got.State, got.DownloadPriority)
	}
}

func TestPriorityUpgradeFailureKeepsReadableFallbackUntilBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	s := client.Series.Create().SetTitle("Priority upgrade").SetSlug("priority-upgrade").SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetImportance(10).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetNumber(1).SaveX(ctx)
	client.ProviderChapter.Create().SetSeriesProvider(low).SetChapterKey("1").SetURL("https://example.test/low").SaveX(ctx)
	policy := settings.Static{Retries: 2, DownloadConc: 1, MaxConcurrentDl: 2}
	good := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: storage}, policy, nil)
	if _, err := good.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	original := client.Chapter.GetX(ctx, ch.ID)
	path := filepath.Join(storage, "Other", s.Title, original.Filename)
	bytes, err := os.ReadFile(path) //nolint:gosec // exact fixture path under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	high := client.SeriesProvider.Create().SetSeries(s).SetProvider("102").SetImportance(20).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProvider(high).SetChapterKey("1").SetURL("https://example.test/high").SaveX(ctx)
	bad := download.New(client, fake.New(fake.WithError(sourceengine.ErrNoPages)), sse.NewHub(), download.Config{Storage: storage}, policy, nil)
	for attempt := 1; attempt <= 2; attempt++ {
		client.ProviderChapter.UpdateOne(pc).ClearNextAttemptAt().ExecX(ctx)
		client.Chapter.UpdateOne(ch).SetState(chapter.StateUpgradeAvailable).SetDownloadPriority(true).ExecX(ctx)
		if err := bad.Upgrade(ctx, ch.ID); err != nil {
			t.Fatal(err)
		}
		got := client.Chapter.GetX(ctx, ch.ID)
		assertOriginalPreserved(t, got, original.Filename, low.ID, 10, bytes, path)
		wantPriority := attempt < 2
		if got.DownloadPriority != wantPriority {
			t.Fatalf("attempt%d: priority=%t, want%t", attempt, got.DownloadPriority, wantPriority)
		}
	}
	client.ProviderChapter.UpdateOne(pc).SetAttempts(0).ClearNextAttemptAt().ExecX(ctx)
	client.Chapter.UpdateOne(ch).SetState(chapter.StateUpgradeAvailable).SetDownloadPriority(true).ExecX(ctx)
	if err := good.Upgrade(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if client.Chapter.GetX(ctx, ch.ID).DownloadPriority {
		t.Fatal("successful upgrade retained priority")
	}

}

func TestPriorityUnstartedUpgradeExhaustionVersusTemporaryAndDormant(t *testing.T) {
	for _, reason := range []string{"exhausted", "backoff", "demoted", "removed"} {
		t.Run(reason, func(t *testing.T) {
			ctx := context.Background()
			client := testdb.New(t)
			s := client.Series.Create().SetTitle(reason).SetSlug(reason).SaveX(ctx)
			low := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetImportance(10).SaveX(ctx)
			high := client.SeriesProvider.Create().SetSeries(s).SetProvider("102").SetImportance(20).SaveX(ctx)
			ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(chapter.StateUpgradeAvailable).SetFilename("readable.cbz").SetSatisfiedImportance(10).SetSatisfiedByProviderID(low.ID).SetDownloadPriority(true).SaveX(ctx)
			client.ProviderChapter.Create().SetSeriesProvider(low).SetChapterKey("1").SetURL("https://x/low").SaveX(ctx)
			pc := client.ProviderChapter.Create().SetSeriesProvider(high).SetChapterKey("1").SetURL("https://x/high").SaveX(ctx)
			configureUnstartedPriority(ctx, client, reason, pc, high)
			f := &gateCallCountFetcher{}
			d := download.New(client, f, sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 1, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
			if err := d.Upgrade(ctx, ch.ID); err != nil {
				t.Fatal(err)
			}
			got := client.Chapter.GetX(ctx, ch.ID)
			if f.calls.Load() != 0 || got.State != chapter.StateDownloaded || got.Filename != "readable.cbz" {
				t.Fatal("unstarted resolution fetched or lost readable fallback")
			}
			if want := reason != "exhausted"; got.DownloadPriority != want {
				t.Fatalf("%s priority=%t want%t", reason, got.DownloadPriority, want)
			}
		})
	}
}

func configureUnstartedPriority(ctx context.Context, client *ent.Client, reason string, pc *ent.ProviderChapter, high *ent.SeriesProvider) {
	switch reason {
	case "exhausted":
		client.ProviderChapter.UpdateOne(pc).SetAttempts(1).ExecX(ctx)
	case "backoff":
		client.ProviderChapter.UpdateOne(pc).SetNextAttemptAt(time.Now().Add(time.Hour)).ExecX(ctx)
	case "demoted":
		client.SeriesProvider.UpdateOne(high).SetImportance(5).ExecX(ctx)
	case "removed":
		client.ProviderChapter.DeleteOne(pc).ExecX(ctx)
	}
}

type oldPriorityCompletionKey struct{}

func TestPriorityOldExhaustionCannotClearFreshSnapshot(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("Fresh snapshot").SetSlug("fresh-snapshot").SaveX(ctx)
	low := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetImportance(10).SaveX(ctx)
	high := client.SeriesProvider.Create().SetSeries(s).SetProvider("102").SetImportance(20).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(chapter.StateUpgradeAvailable).SetFilename("readable.cbz").SetSatisfiedImportance(10).SetSatisfiedByProviderID(low.ID).SetDownloadPriority(true).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProvider(high).SetChapterKey("1").SetURL("https://x/high").SaveX(ctx)
	entered, release := make(chan struct{}), make(chan struct{})
	var enter, exit sync.Once
	unblock := func() { exit.Do(func() { close(release) }) }
	defer unblock()
	holdOldPriorityClear(client, &enter, entered, release)
	d := download.New(client, fake.New(fake.WithError(sourceengine.ErrNoPages)), sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 1, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	done := make(chan error, 1)
	joined := false
	defer joinOldPriorityCompletion(unblock, done, &joined)
	go func() { done <- d.Upgrade(context.WithValue(ctx, oldPriorityCompletionKey{}, true), ch.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("exhaustion clear decision not reached")
	}
	svc := downloads.NewService(client)
	if _, err := svc.SetPriority(ctx, s.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.RedownloadChapter(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPriority(ctx, s.ID, true); err != nil {
		t.Fatal(err)
	}
	unblock()
	err := <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if !got.DownloadPriority || got.State != chapter.StateWanted || client.ProviderChapter.GetX(ctx, pc.ID).Attempts != 0 {
		t.Fatal("old exhausted completion cleared fresh snapshot or reset budgets")
	}
}

func holdOldPriorityClear(client *ent.Client, enter *sync.Once, entered, release chan struct{}) {
	client.Use(ent.Hook(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if mutation, ok := m.(*ent.ChapterMutation); ok && ctx.Value(oldPriorityCompletionKey{}) != nil {
				if marked, set := mutation.DownloadPriority(); set && !marked {
					// Hold after terminal state becomes visible. A combined atomic write has
					// already resolved its old marker; the earlier split write has not.
					if state, set := mutation.State(); set && state == chapter.StatePermanentlyFailed {
						value, err := next.Mutate(ctx, m)
						enter.Do(func() { close(entered); <-release })
						return value, err
					}
					enter.Do(func() { close(entered); <-release })
				}
			}
			return next.Mutate(ctx, m)
		})
	}))
}

func joinOldPriorityCompletion(unblock func(), done <-chan error, joined *bool) {
	unblock()
	if !*joined {
		<-done
	}
}

func TestPriorityOldDownloadExhaustionCannotClearFreshSnapshot(t *testing.T) {
	for _, path := range []string{"owned", "unstarted"} {
		t.Run(path, func(t *testing.T) { checkOldDownloadExhaustion(t, path) })
	}
}

func checkOldDownloadExhaustion(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle(path).SetSlug(path).SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetImportance(10).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetDownloadPriority(true).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://x/high").SaveX(ctx)
	if path == "unstarted" {
		client.ProviderChapter.UpdateOne(pc).SetAttempts(1).ExecX(ctx)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var enter, exit sync.Once
	unblock := func() { exit.Do(func() { close(release) }) }
	defer unblock()
	holdOldPriorityClear(client, &enter, entered, release)
	d := download.New(client, fake.New(fake.WithError(sourceengine.ErrNoPages)), sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 1, DownloadConc: 1, MaxConcurrentDl: 2}, nil)
	done := make(chan error, 1)
	joined := false
	defer joinOldPriorityCompletion(unblock, done, &joined)
	go func() { done <- d.Process(context.WithValue(ctx, oldPriorityCompletionKey{}, true), ch.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("old terminal cleanup not reached")
	}
	owner := make(chan error, 1)
	go func() { owner <- replacePriorityWithRetry(ctx, downloads.NewService(client), s.ID, ch.ID) }()
	waitOwnerWhileOldClearHeld(t, owner, unblock)
	err := <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
	got := client.Chapter.GetX(ctx, ch.ID)
	if !got.DownloadPriority || got.State != chapter.StateWanted || client.ProviderChapter.GetX(ctx, pc.ID).Attempts != 0 {
		t.Fatal("old terminal exhaustion cleared fresh wanted snapshot")
	}
}

func replacePriorityWithRetry(ctx context.Context, svc *downloads.Service, seriesID, chapterID uuid.UUID) error {
	if _, err := svc.SetPriority(ctx, seriesID, false); err != nil {
		return err
	}
	if err := svc.RetryChapter(ctx, chapterID); err != nil {
		return err
	}
	_, err := svc.SetPriority(ctx, seriesID, true)
	return err
}

func waitOwnerWhileOldClearHeld(t *testing.T, owner <-chan error, unblock func()) {
	t.Helper()
	select {
	case err := <-owner:
		unblock()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		unblock()
		if err := <-owner; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPriorityExhaustionDecisionCannotCaptureLaterOwnerGeneration(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	s := client.Series.Create().SetTitle("old-decision").SetSlug("old-decision").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetImportance(10).SaveX(ctx)
	ch := client.Chapter.Create().SetSeries(s).SetChapterKey("1").SetState(chapter.StateFailed).SetDownloadPriority(true).SaveX(ctx)
	pc := client.ProviderChapter.Create().SetSeriesProvider(sp).SetChapterKey("1").SetURL("https://x/high").SetAttempts(1).SaveX(ctx)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	holdExhaustedProviderRead(client, entered, release)
	d := download.New(client, fake.New(), sse.NewHub(), download.Config{Storage: t.TempDir()}, settings.Static{Retries: 1}, nil)
	done := make(chan error, 1)
	joined := false
	defer joinOldPriorityCompletion(unblock, done, &joined)
	go func() { done <- d.Process(context.WithValue(ctx, oldPriorityCompletionKey{}, true), ch.ID) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("exhaustion read not reached")
	}
	if err := replacePriorityWithRetry(ctx, downloads.NewService(client), s.ID, ch.ID); err != nil {
		t.Fatal(err)
	}
	unblock()
	<-done
	joined = true
	got := client.Chapter.GetX(ctx, ch.ID)
	if got.State != chapter.StateWanted || !got.DownloadPriority || client.ProviderChapter.GetX(ctx, pc.ID).Attempts != 0 {
		t.Fatal("old exhaustion decision adopted later owner generation")
	}
}

func holdExhaustedProviderRead(client *ent.Client, entered, release chan struct{}) {
	var reads atomic.Int64
	client.Intercept(ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, q ent.Query) (ent.Value, error) {
			result, err := next.Query(ctx, q)
			if _, ok := q.(*ent.ProviderChapterQuery); ok && ctx.Value(oldPriorityCompletionKey{}) != nil && reads.Add(1) == 3 {
				close(entered)
				<-release
			}
			return result, err
		})
	}))
}
