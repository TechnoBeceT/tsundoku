package imports

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchInFlightCapacityRetainsCancelledPhysicalWork(t *testing.T) {
	c := newSearchCache(func(context.Context) time.Duration { return 0 })
	entered := make(chan struct{}, 128)
	release := make(chan struct{})
	defer close(release)
	callersDone := make(chan error, 128)
	contexts := make([]context.CancelFunc, 128)
	fetch := func(ctx context.Context, _ func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error) {
		entered <- struct{}{}
		<-release
		return nil, ctx.Err()
	}
	for i := range 128 {
		ctx, cancel := context.WithCancel(context.Background())
		contexts[i] = cancel
		defer cancel()
		go func() { _, err := c.Get(ctx, fmt.Sprint(i), nil, fetch, nil); callersDone <- err }()
	}
	for range 128 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("capacity not admitted")
		}
	}
	for _, cancel := range contexts {
		cancel()
	}
	for range 128 {
		if err := <-callersDone; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	// Cancellation releases callers, but noncooperative physical work still occupies its bound.
	called := false
	_, err := c.Get(context.Background(), "overflow", nil, func(context.Context, func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error) {
		called = true
		return nil, nil
	}, nil)
	if !errors.Is(err, errSearchCapacity) || called {
		t.Fatalf("capacity bypass: err=%v called=%v", err, called)
	}
}

func TestSearchFinishedFlightFreshness(t *testing.T) {
	for _, ttl := range []time.Duration{0, time.Second} {
		t.Run(ttl.String(), func(t *testing.T) { checkFinishedFlightFreshness(t, ttl) })
	}
}

func checkFinishedFlightFreshness(t *testing.T, ttl time.Duration) {
	t.Helper()
	var clock atomic.Int64
	c := newSearchCache(func(context.Context) time.Duration { return ttl })
	c.now = func() time.Time { return time.Unix(0, clock.Load()) }
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan error, 1)
	var calls atomic.Int32
	fetch := func(context.Context, func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error) {
		title := "old"
		if calls.Add(1) > 1 {
			title = "fresh"
		}
		return []SearchGroupDTO{{Title: title}}, nil
	}
	go func() {
		_, err := c.Get(context.Background(), "same", nil, fetch, func(snapshot SearchSnapshotDTO) error {
			if snapshot.Done {
				close(entered)
				<-release
			}
			return nil
		})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("terminal writer not reached")
	}
	clock.Store(int64(2 * time.Second))
	groups, err := c.Get(context.Background(), "same", nil, fetch, nil)
	assertFreshSearch(t, groups, err, calls.Load(), 2)
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Reading again proves the old terminal writer cannot overwrite the newer memo.
	groups, err = c.Get(context.Background(), "same", nil, fetch, nil)
	wantCalls := int32(2)
	if ttl == 0 {
		wantCalls = 3
	}
	assertFreshSearch(t, groups, err, calls.Load(), wantCalls)
}

func assertFreshSearch(t *testing.T, groups []SearchGroupDTO, err error, calls, wantCalls int32) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if calls != wantCalls || len(groups) != 1 || groups[0].Title != "fresh" {
		t.Fatalf("fresh demand: calls=%d want=%d groups=%+v", calls, wantCalls, groups)
	}
}
