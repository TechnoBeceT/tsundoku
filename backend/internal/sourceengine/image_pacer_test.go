package sourceengine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakePacerClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
}

func (c *fakePacerClock) current() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakePacerClock) wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.waits = append(c.waits, delay)
	c.now = c.now.Add(delay)
	c.mu.Unlock()
	return nil
}

func (c *fakePacerClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.waits...)
}

func TestImagePacer_FirstRequestDoesNotWait(t *testing.T) {
	clock := &fakePacerClock{now: time.Unix(100, 0)}
	p := newImagePacer(clock.current, clock.wait)

	if err := p.Wait(context.Background(), 7, 750*time.Millisecond); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if got := clock.recorded(); len(got) != 0 {
		t.Fatalf("waits = %v, want none", got)
	}
}

func TestImagePacer_SameSourceReservationsAreSpaced(t *testing.T) {
	clock := &fakePacerClock{now: time.Unix(100, 0)}
	p := newImagePacer(clock.current, clock.wait)

	if err := p.Wait(context.Background(), 7, 750*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), 7, 750*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := clock.recorded(); len(got) != 1 || got[0] != 750*time.Millisecond {
		t.Fatalf("waits = %v, want [750ms]", got)
	}
}

func TestImagePacer_ConcurrentSameSourceReservationsAreSerialized(t *testing.T) {
	clock := &fakePacerClock{now: time.Unix(100, 0)}
	p := newImagePacer(clock.current, clock.wait)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errs <- p.Wait(context.Background(), 7, 750*time.Millisecond)
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := clock.recorded(); len(got) != 1 || got[0] != 750*time.Millisecond {
		t.Fatalf("waits = %v, want one 750ms wait", got)
	}
}

func TestImagePacer_DifferentSourcesDoNotShareReservations(t *testing.T) {
	clock := &fakePacerClock{now: time.Unix(100, 0)}
	p := newImagePacer(clock.current, clock.wait)

	if err := p.Wait(context.Background(), 7, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), 8, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := clock.recorded(); len(got) != 0 {
		t.Fatalf("waits = %v, want none", got)
	}
}

func TestImagePacer_ZeroIsImmediateAndPreservesExistingReservation(t *testing.T) {
	clock := &fakePacerClock{now: time.Unix(100, 0)}
	p := newImagePacer(clock.current, clock.wait)

	if err := p.Wait(context.Background(), 7, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), 7, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), 7, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := clock.recorded(); len(got) != 1 || got[0] != time.Second {
		t.Fatalf("waits = %v, want [1s]", got)
	}
}

func TestImagePacer_NewDelayAppliesWithoutRewritingEarlierReservation(t *testing.T) {
	clock := &fakePacerClock{now: time.Unix(100, 0)}
	p := newImagePacer(clock.current, clock.wait)

	if err := p.Wait(context.Background(), 7, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), 7, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(context.Background(), 7, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := clock.recorded(); len(got) != 2 || got[0] != time.Second || got[1] != 250*time.Millisecond {
		t.Fatalf("waits = %v, want [1s 250ms]", got)
	}
}

func TestImagePacer_CancellationAbandonsWait(t *testing.T) {
	entered := make(chan struct{})
	p := newImagePacer(func() time.Time { return time.Unix(100, 0) }, func(ctx context.Context, _ time.Duration) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	if err := p.Wait(context.Background(), 7, time.Second); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- p.Wait(ctx, 7, time.Second) }()
	<-entered
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
}
