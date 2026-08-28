package job_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
	"github.com/technobecet/tsundoku/internal/sse"
)

type countingThroughputSnapshot struct{ calls atomic.Int64 }

func (s *countingThroughputSnapshot) Snapshot(context.Context) (map[int64]sourcethroughput.Override, error) {
	s.calls.Add(1)
	one := 1
	return map[int64]sourcethroughput.Override{101: {DownloadConcurrency: &one}}, nil
}

func TestRunDownloadCycleCapturesThroughputPolicyOnceAcrossDrainAndUpgrade(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	hub := sse.NewHub()
	storage := t.TempDir()
	s := client.Series.Create().SetTitle("cycle snapshot").SetSlug("cycle-snapshot").SaveX(ctx)
	sp := client.SeriesProvider.Create().SetSeries(s).SetProvider("101").SetProviderName("source-a").SetImportance(10).SaveX(ctx)
	for i := range 5 {
		key := string(rune('a' + i))
		client.ProviderChapter.Create().SetSeriesProviderID(sp.ID).SetChapterKey(key).SetURL("https://source-a/" + key).SetProviderIndex(i).SaveX(ctx)
		client.Chapter.Create().SetSeries(s).SetChapterKey(key).SaveX(ctx)
	}

	store := &countingThroughputSnapshot{}
	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 3, Backoff: time.Hour, DownloadConc: 4, MaxConcurrentDl: 4}, nil).
		WithSourceThroughputPolicies(store)
	runner := job.NewRunner(d, client, hub, storage, settings.Static{})

	if err := runner.RunDownloadCycle(ctx); err != nil {
		t.Fatalf("RunDownloadCycle: %v", err)
	}
	if got := store.calls.Load(); got != 1 {
		t.Fatalf("throughput Snapshot calls = %d, want exactly 1 for the whole cycle", got)
	}
}
