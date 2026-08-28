package sourcethroughput_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/sourcethroughput"
)

type defaults struct {
	concurrency int
	delay       time.Duration
}

func (d defaults) DownloadConcurrency(context.Context) int { return d.concurrency }
func (d defaults) ImageRequestDelay(context.Context) time.Duration {
	return d.delay
}

func newService(t *testing.T) (*sourcethroughput.Service, *ent.Client) {
	t.Helper()
	client := testdb.New(t)
	return sourcethroughput.NewService(client, defaults{concurrency: 5, delay: 500 * time.Millisecond}), client
}

func TestResolveInheritsDefaultsWithoutStoredOverride(t *testing.T) {
	svc, _ := newService(t)

	got, err := svc.Resolve(context.Background(), 101)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.DownloadConcurrency != 5 || got.ImageRequestDelay != 500*time.Millisecond {
		t.Fatalf("Resolve = %+v, want concurrency 5 and delay 500ms", got)
	}
}

func TestExplicitZeroDelayOverridesGlobalDelay(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	gotOverride, err := svc.Update(ctx, 101, sourcethroughput.Patch{
		ImageRequestDelay: sourcethroughput.Set(time.Duration(0)),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if gotOverride.ImageRequestDelay == nil || *gotOverride.ImageRequestDelay != 0 {
		t.Fatalf("stored delay = %v, want pointer to explicit zero", gotOverride.ImageRequestDelay)
	}

	got, err := svc.Resolve(ctx, 101)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.DownloadConcurrency != 5 || got.ImageRequestDelay != 0 {
		t.Fatalf("Resolve = %+v, want inherited concurrency 5 and explicit delay 0", got)
	}

	delay, err := svc.ImageRequestDelay(ctx, 101)
	if err != nil {
		t.Fatalf("ImageRequestDelay: %v", err)
	}
	if delay != got.ImageRequestDelay {
		t.Fatalf("ImageRequestDelay = %v, Resolve delay = %v", delay, got.ImageRequestDelay)
	}
}

func TestPartialUpdateDoesNotClobberOtherOverride(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	_, err := svc.Update(ctx, 101, sourcethroughput.Patch{
		DownloadConcurrency: sourcethroughput.Set(2),
		ImageRequestDelay:   sourcethroughput.Set(750 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("initial Update: %v", err)
	}

	got, err := svc.Update(ctx, 101, sourcethroughput.Patch{
		ImageRequestDelay: sourcethroughput.Set(time.Second),
	})
	if err != nil {
		t.Fatalf("partial Update: %v", err)
	}
	if got.DownloadConcurrency == nil || *got.DownloadConcurrency != 2 {
		t.Fatalf("download concurrency = %v, want preserved value 2", got.DownloadConcurrency)
	}
	if got.ImageRequestDelay == nil || *got.ImageRequestDelay != time.Second {
		t.Fatalf("image delay = %v, want updated value 1s", got.ImageRequestDelay)
	}
}

func TestClearOneKeepsOtherAndClearLastDeletesRow(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()

	_, err := svc.Update(ctx, 101, sourcethroughput.Patch{
		DownloadConcurrency: sourcethroughput.Set(2),
		ImageRequestDelay:   sourcethroughput.Set(750 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("initial Update: %v", err)
	}

	got, err := svc.Update(ctx, 101, sourcethroughput.Patch{
		DownloadConcurrency: sourcethroughput.Clear[int](),
	})
	if err != nil {
		t.Fatalf("clear concurrency: %v", err)
	}
	if got.DownloadConcurrency != nil {
		t.Fatalf("download concurrency = %v, want nil", got.DownloadConcurrency)
	}
	if got.ImageRequestDelay == nil || *got.ImageRequestDelay != 750*time.Millisecond {
		t.Fatalf("image delay = %v, want preserved 750ms", got.ImageRequestDelay)
	}
	if n := client.SourceThroughputPolicy.Query().CountX(ctx); n != 1 {
		t.Fatalf("row count after clearing one override = %d, want 1", n)
	}

	got, err = svc.Update(ctx, 101, sourcethroughput.Patch{
		ImageRequestDelay: sourcethroughput.Clear[time.Duration](),
	})
	if err != nil {
		t.Fatalf("clear last override: %v", err)
	}
	if got.DownloadConcurrency != nil || got.ImageRequestDelay != nil {
		t.Fatalf("cleared override = %+v, want empty", got)
	}
	if n := client.SourceThroughputPolicy.Query().CountX(ctx); n != 0 {
		t.Fatalf("row count after clearing last override = %d, want 0", n)
	}
}

func TestInvalidUpdatesLeaveExistingPolicyUnchanged(t *testing.T) {
	tests := []struct {
		name  string
		patch sourcethroughput.Patch
	}{
		{name: "zero concurrency", patch: sourcethroughput.Patch{DownloadConcurrency: sourcethroughput.Set(0)}},
		{name: "concurrency above global maximum", patch: sourcethroughput.Patch{DownloadConcurrency: sourcethroughput.Set(33)}},
		{name: "negative delay", patch: sourcethroughput.Patch{ImageRequestDelay: sourcethroughput.Set(-time.Millisecond)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newService(t)
			ctx := context.Background()
			_, err := svc.Update(ctx, 101, sourcethroughput.Patch{
				DownloadConcurrency: sourcethroughput.Set(2),
				ImageRequestDelay:   sourcethroughput.Set(750 * time.Millisecond),
			})
			if err != nil {
				t.Fatalf("seed Update: %v", err)
			}

			if _, err := svc.Update(ctx, 101, tt.patch); !errors.Is(err, sourcethroughput.ErrInvalidPolicy) {
				t.Fatalf("Update error = %v, want ErrInvalidPolicy", err)
			}

			got, err := svc.Snapshot(ctx)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			policy := got[101]
			if policy.DownloadConcurrency == nil || *policy.DownloadConcurrency != 2 ||
				policy.ImageRequestDelay == nil || *policy.ImageRequestDelay != 750*time.Millisecond {
				t.Fatalf("policy changed after invalid update: %+v", policy)
			}
		})
	}
}

type countingDriver struct {
	dialect.Driver
	queries atomic.Int64
}

func (d *countingDriver) Query(ctx context.Context, query string, args, v any) error {
	d.queries.Add(1)
	return d.Driver.Query(ctx, query, args, v)
}

func TestSnapshotLoadsAllOverridesInOneQuery(t *testing.T) {
	_, db := testdb.NewWithSQL(t)
	driver := &countingDriver{Driver: entsql.OpenDB(dialect.Postgres, db)}
	client := ent.NewClient(ent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()

	client.SourceThroughputPolicy.Create().
		SetSourceID(101).
		SetDownloadConcurrency(1).
		SaveX(ctx)
	client.SourceThroughputPolicy.Create().
		SetSourceID(202).
		SetImageRequestDelayMs(0).
		SaveX(ctx)
	driver.queries.Store(0)

	got, err := sourcethroughput.NewService(client, defaults{concurrency: 5, delay: 500 * time.Millisecond}).Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if queries := driver.queries.Load(); queries != 1 {
		t.Fatalf("Snapshot query count = %d, want exactly 1", queries)
	}
	if len(got) != 2 || got[101].DownloadConcurrency == nil || *got[101].DownloadConcurrency != 1 {
		t.Fatalf("Snapshot[101] = %+v, full snapshot = %+v", got[101], got)
	}
	if got[202].ImageRequestDelay == nil || *got[202].ImageRequestDelay != 0 {
		t.Fatalf("Snapshot[202] = %+v, want explicit zero delay", got[202])
	}
}
