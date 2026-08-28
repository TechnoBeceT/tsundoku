package sourceengine

import (
	"context"
	"sync"
	"time"
)

// imagePacer coordinates request-start reservations across every chapter using
// the same live source. Its mutex protects only reservation arithmetic; waits
// happen after unlocking so one slow source never blocks another source.
type imagePacer struct {
	mu   sync.Mutex
	next map[int64]time.Time
	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

func newImagePacer(now func() time.Time, wait func(context.Context, time.Duration) error) *imagePacer {
	return &imagePacer{next: make(map[int64]time.Time), now: now, wait: wait}
}

func newRealtimeImagePacer() *imagePacer {
	return newImagePacer(time.Now, func(ctx context.Context, delay time.Duration) error {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	})
}

// Wait atomically reserves this request's start for sourceID using the delay
// observed by its caller. Zero disables pacing for this request and deliberately
// leaves any prior reservations intact.
func (p *imagePacer) Wait(ctx context.Context, sourceID int64, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}

	p.mu.Lock()
	now := p.now()
	start := now
	if reserved := p.next[sourceID]; reserved.After(start) {
		start = reserved
	}
	p.next[sourceID] = start.Add(delay)
	p.mu.Unlock()

	if wait := start.Sub(now); wait > 0 {
		return p.wait(ctx, wait)
	}
	return nil
}
