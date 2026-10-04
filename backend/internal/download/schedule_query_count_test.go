package download_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	"github.com/technobecet/tsundoku/internal/sourcegate"
	"github.com/technobecet/tsundoku/internal/sse"
)

func TestRunOnceWaitingReadCountIsBatchIndependent(t *testing.T) {
	for _, mode := range []string{"breaker", "backoff"} {
		t.Run(mode, func(t *testing.T) {
			var counts []int64
			for _, n := range []int{1, 20} {
				counts = append(counts, waitingBatchReads(t, mode, n))
			}
			t.Logf("waiting reads for 1/20 chapters: %v", counts)
			if counts[0] != counts[1] {
				t.Errorf("waiting reads grew with batch: %v", counts)
			}
		})
	}
}

func waitingBatchReads(t *testing.T, mode string, n int) int64 {
	t.Helper()
	ctx := context.Background()
	_, db := testdb.NewWithSQL(t)
	client, counter := newBatchCountingClient(db)
	provider := fmt.Sprintf("waiting-%s", mode)
	chapters := oneSourceSeries(ctx, t, client, provider, n)
	now := time.Now()
	until := now.Add(time.Hour)
	if mode == "breaker" {
		client.SourceCircuitState.Create().SetSourceKey(provider).SetCooldownUntil(until).SaveX(ctx)
	} else {
		for _, ch := range chapters {
			client.ProviderChapter.Update().Where(entproviderchapter.ChapterKeyEQ(ch.ChapterKey)).SetNextAttemptAt(until).ExecX(ctx)
		}
	}
	opts := gateTestSettings(2, time.Hour)
	f := &gateCallCountFetcher{}
	d := download.New(client, f, sse.NewHub(), download.Config{Storage: t.TempDir()}, opts, sourcegate.NewService(client, opts))
	counter.queries.Store(0)
	progressed, err := d.RunOnceAt(ctx, now, nil, nil)
	if err != nil || progressed != 0 || f.calls.Load() != 0 {
		t.Fatalf("waiting batch dispatched: progressed=%d calls=%d err=%v", progressed, f.calls.Load(), err)
	}
	return counter.queries.Load()
}
