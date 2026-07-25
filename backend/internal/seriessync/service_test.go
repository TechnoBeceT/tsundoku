// Package seriessync_test verifies the per-series instant convergence
// orchestrator (GAP-113): ordering (refresh → detect → trigger), the detect-only
// path, single-flight per series, per-series keying (different series run), and
// nil-trigger safety. It uses in-memory fakes — no DB, no engine — so it is fast
// and deterministic.
package seriessync_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/refresh"
	"github.com/technobecet/tsundoku/internal/seriessync"
)

// recorder captures the call order across the fakes under a lock.
type recorder struct {
	mu    sync.Mutex
	order []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.order = append(r.order, s)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// fakeRefresher records a RefreshSeries call and optionally blocks on `block`
// (held in flight) so the single-flight guard can be exercised deterministically.
type fakeRefresher struct {
	rec   *recorder
	block chan struct{}
	calls atomic.Int32
}

func (f *fakeRefresher) RefreshSeries(ctx context.Context, _ uuid.UUID) (refresh.RefreshResult, error) {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
		}
	}
	f.calls.Add(1)
	f.rec.add("refresh")
	return refresh.RefreshResult{}, nil
}

// fakeDetector records a DetectUpgradesForSeries call and the maxRetries it was
// handed (to prove the orchestrator sources it from MaxRetries).
type fakeDetector struct {
	rec        *recorder
	maxRetries int
	gotMax     atomic.Int64
	calls      atomic.Int32
}

func (f *fakeDetector) DetectUpgradesForSeries(_ context.Context, _ uuid.UUID, maxRetries int) (int, error) {
	f.gotMax.Store(int64(maxRetries))
	f.calls.Add(1)
	f.rec.add("detect")
	return 0, nil
}

func (f *fakeDetector) MaxRetries(context.Context) int { return f.maxRetries }

func equalSeq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSyncSeries_RefreshThenDetectThenTrigger pins the SyncSeries order and that
// the detector's maxRetries comes from MaxRetries.
func TestSyncSeries_RefreshThenDetectThenTrigger(t *testing.T) {
	rec := &recorder{}
	r := &fakeRefresher{rec: rec}
	d := &fakeDetector{rec: rec, maxRetries: 7}
	triggered := make(chan struct{}, 1)
	o := seriessync.NewOrchestrator(r, d, func() {
		rec.add("trigger")
		triggered <- struct{}{}
	})

	o.SyncSeries(context.Background(), uuid.New())

	select {
	case <-triggered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SyncSeries to complete")
	}

	if got := rec.snapshot(); !equalSeq(got, []string{"refresh", "detect", "trigger"}) {
		t.Errorf("call order = %v, want [refresh detect trigger]", got)
	}
	if d.gotMax.Load() != 7 {
		t.Errorf("detector maxRetries = %d, want 7 (from MaxRetries)", d.gotMax.Load())
	}
}

// TestDetectSeries_DetectThenTrigger_NoRefresh pins the re-rank path: detection +
// trigger, and NO feed re-fetch.
func TestDetectSeries_DetectThenTrigger_NoRefresh(t *testing.T) {
	rec := &recorder{}
	r := &fakeRefresher{rec: rec}
	d := &fakeDetector{rec: rec, maxRetries: 3}
	triggered := make(chan struct{}, 1)
	o := seriessync.NewOrchestrator(r, d, func() {
		rec.add("trigger")
		triggered <- struct{}{}
	})

	o.DetectSeries(context.Background(), uuid.New())

	select {
	case <-triggered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for DetectSeries to complete")
	}

	if got := rec.snapshot(); !equalSeq(got, []string{"detect", "trigger"}) {
		t.Errorf("call order = %v, want [detect trigger]", got)
	}
	if r.calls.Load() != 0 {
		t.Errorf("RefreshSeries called %d times on the detect-only path, want 0", r.calls.Load())
	}
}

// TestSingleFlight_ConcurrentSameSeriesRunsOnce proves that concurrent triggers for
// the SAME series coalesce to one run: two extra Sync calls fired while the first is
// in flight are dropped.
func TestSingleFlight_ConcurrentSameSeriesRunsOnce(t *testing.T) {
	rec := &recorder{}
	block := make(chan struct{})
	r := &fakeRefresher{rec: rec, block: block}
	d := &fakeDetector{rec: rec, maxRetries: 3}
	triggered := make(chan struct{}, 4)
	o := seriessync.NewOrchestrator(r, d, func() { triggered <- struct{}{} })

	id := uuid.New()
	ctx := context.Background()
	// The first call acquires the latch synchronously and its goroutine blocks in
	// RefreshSeries; the next two see the latch held and are dropped.
	o.SyncSeries(ctx, id)
	o.SyncSeries(ctx, id)
	o.SyncSeries(ctx, id)

	close(block) // let the first (only) run proceed to completion

	select {
	case <-triggered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the single run to complete")
	}
	// No second run may execute.
	select {
	case <-triggered:
		t.Fatal("a second run executed — single-flight guard violated")
	case <-time.After(100 * time.Millisecond):
	}

	if r.calls.Load() != 1 {
		t.Errorf("RefreshSeries ran %d times, want 1 (single-flight)", r.calls.Load())
	}
	if d.calls.Load() != 1 {
		t.Errorf("DetectUpgradesForSeries ran %d times, want 1 (single-flight)", d.calls.Load())
	}
}

// TestDifferentSeriesRunConcurrently proves the latch is keyed per series: two
// different series both run (not mutually excluded).
func TestDifferentSeriesRunConcurrently(t *testing.T) {
	rec := &recorder{}
	r := &fakeRefresher{rec: rec}
	d := &fakeDetector{rec: rec, maxRetries: 3}
	triggered := make(chan struct{}, 2)
	o := seriessync.NewOrchestrator(r, d, func() { triggered <- struct{}{} })

	ctx := context.Background()
	o.SyncSeries(ctx, uuid.New())
	o.SyncSeries(ctx, uuid.New())

	for i := 0; i < 2; i++ {
		select {
		case <-triggered:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for both series to run")
		}
	}
	if r.calls.Load() != 2 {
		t.Errorf("RefreshSeries ran %d times, want 2 (one per series)", r.calls.Load())
	}
	if d.calls.Load() != 2 {
		t.Errorf("DetectUpgradesForSeries ran %d times, want 2 (one per series)", d.calls.Load())
	}
}

// TestNilTrigger_NoPanic proves a nil trigger is safe: the run still refreshes +
// detects and simply skips the cycle nudge.
func TestNilTrigger_NoPanic(t *testing.T) {
	rec := &recorder{}
	r := &fakeRefresher{rec: rec}
	done := make(chan struct{})
	d := &detectorSignaller{fakeDetector: fakeDetector{rec: rec, maxRetries: 3}, done: done}
	o := seriessync.NewOrchestrator(r, d, nil)

	o.SyncSeries(context.Background(), uuid.New())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for run with nil trigger")
	}
	if got := rec.snapshot(); !equalSeq(got, []string{"refresh", "detect"}) {
		t.Errorf("call order = %v, want [refresh detect] (nil trigger skipped)", got)
	}
}

// detectorSignaller closes done after recording its detect, so a nil-trigger test
// can wait for completion without a trigger to signal on.
type detectorSignaller struct {
	fakeDetector
	done chan struct{}
	once sync.Once
}

func (f *detectorSignaller) DetectUpgradesForSeries(ctx context.Context, id uuid.UUID, maxRetries int) (int, error) {
	n, err := f.fakeDetector.DetectUpgradesForSeries(ctx, id, maxRetries)
	f.once.Do(func() { close(f.done) })
	return n, err
}
