package download

import (
	"context"
	"testing"
	"time"
)

func TestFetchCapacityDecreaseDrainsExistingPermitsBeforeAdmitting(t *testing.T) {
	capacity := newFetchCapacity(2)
	for range 2 {
		if err := capacity.Acquire(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	capacity.setLimit(1)
	started := make(chan struct{})
	go func() { _ = capacity.Acquire(context.Background(), 1); close(started) }()
	assertFetchCapacityWaiting(t, started)
	capacity.Release(1)
	assertFetchCapacityWaiting(t, started)
	capacity.Release(1)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("capacity did not admit after old permits drained")
	}
	capacity.Release(1)
}

func assertFetchCapacityWaiting(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
		t.Fatal("lowered bound admitted while old permits filled its new limit")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestProviderLimiterPolicyDecreaseKeepsOutstandingSourcePermits(t *testing.T) {
	limiter := newPolicyProviderLimiter(sourceConcurrencyPolicy{global: 2})
	releaseA, err := limiter.acquire(context.Background(), "same-physical-source", 101)
	if err != nil {
		t.Fatal(err)
	}
	releaseB, err := limiter.acquire(context.Background(), "same-physical-source", 101)
	if err != nil {
		t.Fatal(err)
	}
	limiter.setPolicy(sourceConcurrencyPolicy{global: 2, overrides: map[int64]int{101: 1}})
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		release, _ := limiter.acquire(context.Background(), "same-physical-source", 101)
		close(started)
		<-done
		release()
	}()
	assertFetchCapacityWaiting(t, started)
	releaseA()
	assertFetchCapacityWaiting(t, started)
	releaseB()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("source did not resume after original permits drained")
	}
	close(done)
}
