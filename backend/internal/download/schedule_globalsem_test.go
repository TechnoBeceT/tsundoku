// White-box tests for the GLOBAL concurrency cap threaded through
// runPerSourceQueues (schedule.go). They drive the scheduler directly with a
// synthetic run function and an in-flight peak tracker, so they need no database
// or fetcher — the invariant under test is purely the scheduler's own concurrency
// arithmetic: with a non-nil semaphore the TOTAL simultaneous run executions
// across ALL sources never exceed the cap, while the per-source cap and
// run-everything guarantees are preserved; with a nil semaphore behaviour is
// unchanged from the historical per-source-only scheduling.
package download

import (
	"context"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/semaphore"
)

// concurrencyTracker records the peak simultaneous run executions overall and per
// source, plus the total number of runs, under concurrent access.
type concurrencyTracker struct {
	mu            sync.Mutex
	globalCur     int
	globalPeak    int
	perSourceCur  map[string]int
	perSourcePeak map[string]int
	totalRuns     int
}

func newConcurrencyTracker() *concurrencyTracker {
	return &concurrencyTracker{
		perSourceCur:  make(map[string]int),
		perSourcePeak: make(map[string]int),
	}
}

// enter/leave bracket one run execution, updating the live counters and peaks.
func (c *concurrencyTracker) enter(source string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.globalCur++
	if c.globalCur > c.globalPeak {
		c.globalPeak = c.globalCur
	}
	c.perSourceCur[source]++
	if c.perSourceCur[source] > c.perSourcePeak[source] {
		c.perSourcePeak[source] = c.perSourceCur[source]
	}
	c.totalRuns++
}

func (c *concurrencyTracker) leave(source string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.globalCur--
	c.perSourceCur[source]--
}

// schedItem carries its source key so the synthetic run function can attribute
// each execution to a source for per-source peak tracking.
type schedItem struct{ source string }

// buildGroups returns numSources groups of itemsPerSource items each, keyed by a
// stable "src-N" source label.
func buildGroups(numSources, itemsPerSource int) map[string][]schedItem {
	groups := make(map[string][]schedItem, numSources)
	for s := 0; s < numSources; s++ {
		key := "src-" + string(rune('a'+s))
		items := make([]schedItem, itemsPerSource)
		for i := range items {
			items[i] = schedItem{source: key}
		}
		groups[key] = items
	}
	return groups
}

// TestRunPerSourceQueues_GlobalCapHolds proves that with a global semaphore the
// observed maximum simultaneous run executions across ALL sources never exceeds
// the cap, the per-source cap is still honoured, and every item runs (no deadlock,
// no dropped work). 6 sources × 10 items, per-source cap 3, global cap 4: the
// per-source caps alone would permit up to 18 in flight (6×3), so a peak that
// stays at or below 4 can only come from the global semaphore.
func TestRunPerSourceQueues_GlobalCapHolds(t *testing.T) {
	const (
		numSources     = 6
		itemsPerSource = 10
		perSourceCap   = 3
		globalCap      = 4
	)
	groups := buildGroups(numSources, itemsPerSource)
	tracker := newConcurrencyTracker()
	sem := semaphore.NewWeighted(int64(globalCap))

	run := func(_ context.Context, it schedItem) error {
		tracker.enter(it.source)
		// A short sleep forces genuine overlap so the peak is meaningful rather
		// than an artefact of runs completing before the next one starts.
		time.Sleep(2 * time.Millisecond)
		tracker.leave(it.source)
		return nil
	}

	if err := runPerSourceQueues(context.Background(), groups, perSourceCap, run, sem); err != nil {
		t.Fatalf("runPerSourceQueues returned error: %v", err)
	}

	if tracker.globalPeak > globalCap {
		t.Errorf("global peak concurrency = %d, must never exceed the cap %d", tracker.globalPeak, globalCap)
	}
	// Overlap sanity: with this much work and the sleep, the cap must actually be
	// exercised — a peak of 1 would mean the scheduler serialised everything and
	// the ≤cap assertion above would pass trivially.
	if tracker.globalPeak < 2 {
		t.Errorf("global peak concurrency = %d, expected real overlap (>= 2)", tracker.globalPeak)
	}
	for source, peak := range tracker.perSourcePeak {
		if peak > perSourceCap {
			t.Errorf("source %s peak concurrency = %d, must never exceed the per-source cap %d", source, peak, perSourceCap)
		}
	}
	if want := numSources * itemsPerSource; tracker.totalRuns != want {
		t.Errorf("total runs = %d, want %d (every item must run — no deadlock, no drops)", tracker.totalRuns, want)
	}
}

// TestRunPerSourceQueues_NilSemaphoreUnchanged proves the nil-semaphore path
// behaves exactly as before the global cap existed: only the per-source cap
// bounds concurrency, and every item still runs. With no global semaphore the
// aggregate peak may (and typically will) exceed any single source's cap.
func TestRunPerSourceQueues_NilSemaphoreUnchanged(t *testing.T) {
	const (
		numSources     = 6
		itemsPerSource = 10
		perSourceCap   = 3
	)
	groups := buildGroups(numSources, itemsPerSource)
	tracker := newConcurrencyTracker()

	run := func(_ context.Context, it schedItem) error {
		tracker.enter(it.source)
		time.Sleep(2 * time.Millisecond)
		tracker.leave(it.source)
		return nil
	}

	if err := runPerSourceQueues(context.Background(), groups, perSourceCap, run, nil); err != nil {
		t.Fatalf("runPerSourceQueues (nil sem) returned error: %v", err)
	}

	// The only concurrency bound with a nil semaphore is the per-source cap.
	for source, peak := range tracker.perSourcePeak {
		if peak > perSourceCap {
			t.Errorf("source %s peak concurrency = %d, must never exceed the per-source cap %d", source, peak, perSourceCap)
		}
	}
	if want := numSources * itemsPerSource; tracker.totalRuns != want {
		t.Errorf("total runs = %d, want %d (every item must run)", tracker.totalRuns, want)
	}
}
