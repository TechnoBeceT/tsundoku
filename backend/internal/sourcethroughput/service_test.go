package sourcethroughput_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/technobecet/tsundoku/internal/database/testdb"
	"github.com/technobecet/tsundoku/internal/ent"
	entpolicy "github.com/technobecet/tsundoku/internal/ent/sourcethroughputpolicy"
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

func TestImageRequestDelayReturnsCurrentGlobalOnPolicyReadFailure(t *testing.T) {
	client := testdb.New(t)
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	for _, tc := range []struct {
		name string
		want time.Duration
	}{
		{name: "non-zero global", want: 500 * time.Millisecond},
		{name: "explicit zero global", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := sourcethroughput.NewService(client, defaults{concurrency: 5, delay: tc.want})
			got, err := svc.ImageRequestDelay(context.Background(), 101)
			if err == nil {
				t.Fatal("ImageRequestDelay error = nil, want policy read error for observability")
			}
			if got != tc.want {
				t.Fatalf("ImageRequestDelay = %v, want current global fallback %v", got, tc.want)
			}
		})
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

type queryBarrierDriver struct {
	dialect.Driver
	mu      sync.Mutex
	arrived int
	release chan struct{}
}

func newQueryBarrierClient(t *testing.T, dbDriver dialect.Driver) *ent.Client {
	t.Helper()
	client := ent.NewClient(ent.Driver(&queryBarrierDriver{
		Driver:  dbDriver,
		release: make(chan struct{}),
	}))
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func (d *queryBarrierDriver) Query(ctx context.Context, query string, args, v any) error {
	if err := d.Driver.Query(ctx, query, args, v); err != nil {
		return err
	}
	if !strings.HasPrefix(strings.TrimSpace(query), "SELECT") {
		return nil
	}

	d.mu.Lock()
	d.arrived++
	arrived := d.arrived
	if arrived == 2 {
		close(d.release)
	}
	d.mu.Unlock()

	if arrived <= 2 {
		select {
		case <-d.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func TestConcurrentDisjointPartialUpdatesDoNotClobber(t *testing.T) {
	seedClient, db := testdb.NewWithSQL(t)
	ctx := context.Background()
	seedClient.SourceThroughputPolicy.Create().
		SetSourceID(101).
		SetDownloadConcurrency(2).
		SetImageRequestDelayMs(750).
		SaveX(ctx)

	client := newQueryBarrierClient(t, entsql.OpenDB(dialect.Postgres, db))
	svc := sourcethroughput.NewService(client, defaults{concurrency: 5, delay: 500 * time.Millisecond})
	runConcurrentUpdates(t,
		func() error {
			_, err := svc.Update(ctx, 101, sourcethroughput.Patch{
				DownloadConcurrency: sourcethroughput.Set(3),
			})
			return err
		},
		func() error {
			_, err := svc.Update(ctx, 101, sourcethroughput.Patch{
				ImageRequestDelay: sourcethroughput.Set(time.Second),
			})
			return err
		},
	)

	got := client.SourceThroughputPolicy.Query().
		Where(entpolicy.SourceID(101)).
		OnlyX(ctx)
	if got.DownloadConcurrency == nil || *got.DownloadConcurrency != 3 ||
		got.ImageRequestDelayMs == nil || *got.ImageRequestDelayMs != 1000 {
		t.Fatalf("concurrent partial updates stored concurrency=%v delay_ms=%v, want 3 and 1000", got.DownloadConcurrency, got.ImageRequestDelayMs)
	}
}

func TestConcurrentFirstWritesMergeWithoutConstraintError(t *testing.T) {
	_, db := testdb.NewWithSQL(t)
	ctx := context.Background()
	client := newQueryBarrierClient(t, entsql.OpenDB(dialect.Postgres, db))
	svc := sourcethroughput.NewService(client, defaults{concurrency: 5, delay: 500 * time.Millisecond})
	runConcurrentUpdates(t,
		func() error {
			_, err := svc.Update(ctx, 202, sourcethroughput.Patch{
				DownloadConcurrency: sourcethroughput.Set(1),
			})
			return err
		},
		func() error {
			_, err := svc.Update(ctx, 202, sourcethroughput.Patch{
				ImageRequestDelay: sourcethroughput.Set(750 * time.Millisecond),
			})
			return err
		},
	)

	got := client.SourceThroughputPolicy.Query().
		Where(entpolicy.SourceID(202)).
		OnlyX(ctx)
	if got.DownloadConcurrency == nil || *got.DownloadConcurrency != 1 ||
		got.ImageRequestDelayMs == nil || *got.ImageRequestDelayMs != 750 {
		t.Fatalf("concurrent first writes stored concurrency=%v delay_ms=%v, want 1 and 750", got.DownloadConcurrency, got.ImageRequestDelayMs)
	}
}

func runConcurrentUpdates(t *testing.T, updates ...func() error) {
	t.Helper()
	start := make(chan struct{})
	errs := make(chan error, len(updates))
	for _, update := range updates {
		go func() {
			<-start
			errs <- update()
		}()
	}
	close(start)
	for range updates {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Update: %v", err)
		}
	}
}

func TestClearOneKeepsOtherAndClearLastDeletesRow(t *testing.T) {
	svc, client := newService(t)
	ctx := context.Background()
	seedSourceThroughputOverride(t, ctx, svc, 101)
	clearConcurrencyAndAssertDelayRemains(t, ctx, svc, client, 101)
	clearDelayAndAssertRowDeleted(t, ctx, svc, client, 101)
}

func seedSourceThroughputOverride(t *testing.T, ctx context.Context, svc *sourcethroughput.Service, sourceID int64) {
	t.Helper()
	_, err := svc.Update(ctx, sourceID, sourcethroughput.Patch{
		DownloadConcurrency: sourcethroughput.Set(2),
		ImageRequestDelay:   sourcethroughput.Set(750 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("initial Update: %v", err)
	}
}

func clearConcurrencyAndAssertDelayRemains(t *testing.T, ctx context.Context, svc *sourcethroughput.Service, client *ent.Client, sourceID int64) {
	t.Helper()
	got, err := svc.Update(ctx, sourceID, sourcethroughput.Patch{
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
}

func clearDelayAndAssertRowDeleted(t *testing.T, ctx context.Context, svc *sourcethroughput.Service, client *ent.Client, sourceID int64) {
	t.Helper()
	got, err := svc.Update(ctx, sourceID, sourcethroughput.Patch{
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

func TestImageRequestDelayAcceptsZeroAndWholeMilliseconds(t *testing.T) {
	tests := []struct {
		name     string
		sourceID int64
		delay    time.Duration
	}{
		{name: "explicit zero", sourceID: 301, delay: 0},
		{name: "whole milliseconds", sourceID: 302, delay: 1250 * time.Millisecond},
	}

	svc, _ := newService(t)
	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := svc.Update(ctx, tt.sourceID, sourcethroughput.Patch{
				ImageRequestDelay: sourcethroughput.Set(tt.delay),
			})
			if err != nil {
				t.Fatalf("Update(%v): %v", tt.delay, err)
			}
			if got.ImageRequestDelay == nil || *got.ImageRequestDelay != tt.delay {
				t.Fatalf("stored delay = %v, want %v", got.ImageRequestDelay, tt.delay)
			}
		})
	}
}

func TestPositiveSubMillisecondImageDelayIsRejectedWithoutMutation(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	_, err := svc.Update(ctx, 303, sourcethroughput.Patch{
		ImageRequestDelay: sourcethroughput.Set(750 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("seed Update: %v", err)
	}

	_, err = svc.Update(ctx, 303, sourcethroughput.Patch{
		ImageRequestDelay: sourcethroughput.Set(500 * time.Microsecond),
	})
	if !errors.Is(err, sourcethroughput.ErrInvalidPolicy) {
		t.Fatalf("Update(500us) error = %v, want ErrInvalidPolicy", err)
	}

	got, err := svc.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	stored := got[303]
	if stored.ImageRequestDelay == nil || *stored.ImageRequestDelay != 750*time.Millisecond {
		t.Fatalf("delay changed after invalid update: %v", stored.ImageRequestDelay)
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

// TestDefaultsAndApplyDefaults prove the API can resolve a whole policy
// snapshot after reading hot-reloadable defaults once, without an N+1 read.
func TestDefaultsAndApplyDefaults(t *testing.T) {
	ctx := context.Background()
	svc := sourcethroughput.NewService(nil, defaults{concurrency: 5, delay: 500 * time.Millisecond})

	global := svc.Defaults(ctx)
	if global.DownloadConcurrency != 5 || global.ImageRequestDelay != 500*time.Millisecond {
		t.Fatalf("Defaults = %#v", global)
	}
	one := 1
	zero := time.Duration(0)
	got := sourcethroughput.ApplyDefaults(global, sourcethroughput.Override{
		DownloadConcurrency: &one,
		ImageRequestDelay:   &zero,
	})
	if got.DownloadConcurrency != 1 || got.ImageRequestDelay != 0 {
		t.Fatalf("ApplyDefaults = %#v", got)
	}
}
