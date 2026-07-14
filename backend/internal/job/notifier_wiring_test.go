package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sse"
)

// countingNotifier records how many times NotifyNewChapters is called and can be
// made to fail, to prove a notifier error never breaks the cycle.
type countingNotifier struct {
	calls int
	err   error
}

func (n *countingNotifier) NotifyNewChapters(context.Context) error {
	n.calls++
	return n.err
}

// TestRunDownloadCycle_CallsNotifier proves the registered notifier is invoked
// exactly once per cycle and that a notifier error does NOT change
// RunDownloadCycle's (nil) result — the notify pass is strictly best-effort.
func TestRunDownloadCycle_CallsNotifier(t *testing.T) {
	ctx := context.Background()
	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage},
		settings.Static{Retries: 3, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})

	n := &countingNotifier{err: errors.New("boom")}
	r.SetNotifier(n)

	if err := r.RunDownloadCycle(ctx); err != nil {
		t.Fatalf("notifier error must not fail the cycle, got: %v", err)
	}
	if n.calls != 1 {
		t.Fatalf("notifier called %d times, want 1", n.calls)
	}
}
