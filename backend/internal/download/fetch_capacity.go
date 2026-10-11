package download

import (
	"context"
	"sync"
)

// FetchSemaphore is the fetch-boundary capacity contract. Fixed-pass callers
// can supply a weighted semaphore; a persistent scheduler uses adjustable
// capacity so runtime tuning does not replace permits held by active fetches.
type FetchSemaphore interface {
	Acquire(context.Context, int64) error
	Release(int64)
}

type fetchCapacity struct {
	mu          sync.Mutex
	limit, used int64
	changed     chan struct{}
}

func newFetchCapacity(limit int) *fetchCapacity {
	return &fetchCapacity{limit: int64(clampConcurrency(limit)), changed: make(chan struct{})}
}

func (c *fetchCapacity) Acquire(ctx context.Context, n int64) error {
	for {
		c.mu.Lock()
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return err
		}
		if c.used+n <= c.limit {
			c.used += n
			c.mu.Unlock()
			return nil
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (c *fetchCapacity) Release(n int64) {
	c.mu.Lock()
	c.used -= n
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}

func (c *fetchCapacity) setLimit(limit int) {
	c.mu.Lock()
	updated := int64(clampConcurrency(limit))
	if c.limit == updated {
		c.mu.Unlock()
		return
	}
	c.limit = updated
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}
