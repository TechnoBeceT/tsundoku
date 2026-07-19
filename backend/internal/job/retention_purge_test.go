package job_test

import (
	"context"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/download"
	entsourceevent "github.com/technobecet/tsundoku/internal/ent/sourceevent"
	"github.com/technobecet/tsundoku/internal/fetcher/fake"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/settings"
	"github.com/technobecet/tsundoku/internal/sourceevents"
	"github.com/technobecet/tsundoku/internal/sse"
)

// TestRunner_StartRetentionPurge_PurgesAtBoot proves the retention ticker runs a
// purge at the TOP of its loop (promptly at boot): an event older than the
// retention window is deleted while a fresh one survives. The purge cadence is
// 24h, so the only way old rows disappear within the test window is the boot
// pass.
func TestRunner_StartRetentionPurge_PurgesAtBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := testdb.New(t)
	storage := t.TempDir()
	hub := sse.NewHub()

	// Seed one old (40 days) + one fresh (1h) event. A 30-day window purges the
	// old one only.
	now := time.Now()
	client.SourceEvent.Create().SetSourceKey("old").
		SetEventType(entsourceevent.EventTypeSearch).SetStatus(entsourceevent.StatusSuccess).
		SetCreatedAt(now.Add(-40 * 24 * time.Hour)).ExecX(ctx)
	client.SourceEvent.Create().SetSourceKey("fresh").
		SetEventType(entsourceevent.EventTypeSearch).SetStatus(entsourceevent.StatusSuccess).
		SetCreatedAt(now.Add(-1 * time.Hour)).ExecX(ctx)

	d := download.New(client, fake.New(), hub, download.Config{Storage: storage}, settings.Static{Retries: 1, Backoff: time.Hour}, nil)
	r := job.NewRunner(d, client, hub, storage, settings.Static{})
	svc := sourceevents.NewService(client)

	r.StartRetentionPurge(ctx, svc, func(context.Context) int { return 30 })

	deadline := time.Now().Add(2 * time.Second)
	for {
		n := client.SourceEvent.Query().Where(entsourceevent.SourceKeyEQ("old")).CountX(ctx)
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("boot retention purge did not delete the old event within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if n := client.SourceEvent.Query().Where(entsourceevent.SourceKeyEQ("fresh")).CountX(ctx); n != 1 {
		t.Fatalf("fresh event should survive the purge, got count %d", n)
	}
}
