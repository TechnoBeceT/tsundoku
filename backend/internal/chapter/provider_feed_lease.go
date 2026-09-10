package chapter

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/semaphore"
)

type leaseEntry struct {
	token chan struct{}
	refs  int
}

type leaseRegistry struct {
	sync.Mutex
	entries map[uuid.UUID]*leaseEntry
}

func newLeaseRegistry() *leaseRegistry {
	return &leaseRegistry{entries: make(map[uuid.UUID]*leaseEntry)}
}

func (r *leaseRegistry) acquire(ctx context.Context, id uuid.UUID) (*exclusiveLease, error) {
	r.Lock()
	entry := r.entries[id]
	if entry == nil {
		entry = &leaseEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		r.entries[id] = entry
	}
	entry.refs++
	r.Unlock()

	select {
	case <-ctx.Done():
		r.releaseReference(id, entry)
		return nil, ctx.Err()
	case <-entry.token:
		return &exclusiveLease{release: func() {
			entry.token <- struct{}{}
			r.releaseReference(id, entry)
		}}, nil
	}
}

func (r *leaseRegistry) releaseReference(id uuid.UUID, entry *leaseEntry) {
	r.Lock()
	defer r.Unlock()
	entry.refs--
	if entry.refs == 0 && r.entries[id] == entry {
		delete(r.entries, id)
	}
}

const providerChapterLeaseCapacity int64 = 1 << 62

type sharedLeaseEntry struct {
	sem  *semaphore.Weighted
	refs int
}

type sharedLeaseRegistry struct {
	sync.Mutex
	entries map[uuid.UUID]*sharedLeaseEntry
}

func newSharedLeaseRegistry() *sharedLeaseRegistry {
	return &sharedLeaseRegistry{entries: make(map[uuid.UUID]*sharedLeaseEntry)}
}

func (r *sharedLeaseRegistry) acquire(ctx context.Context, id uuid.UUID, weight int64) (*exclusiveLease, error) {
	r.Lock()
	entry := r.entries[id]
	if entry == nil {
		entry = &sharedLeaseEntry{sem: semaphore.NewWeighted(providerChapterLeaseCapacity)}
		r.entries[id] = entry
	}
	entry.refs++
	r.Unlock()

	if err := entry.sem.Acquire(ctx, weight); err != nil {
		r.releaseReference(id, entry)
		return nil, err
	}
	return &exclusiveLease{release: func() {
		entry.sem.Release(weight)
		r.releaseReference(id, entry)
	}}, nil
}

func (r *sharedLeaseRegistry) releaseReference(id uuid.UUID, entry *sharedLeaseEntry) {
	r.Lock()
	defer r.Unlock()
	entry.refs--
	if entry.refs == 0 && r.entries[id] == entry {
		delete(r.entries, id)
	}
}

type exclusiveLease struct {
	release func()
	once    sync.Once
}

func (l *exclusiveLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(l.release)
}

var (
	providerReconcileLeases = newLeaseRegistry()
	providerChapterLeases   = newSharedLeaseRegistry()
)

// ProviderFeedLease gives one reconcile exclusive ownership against other
// reconciles of the same SeriesProvider. Individual download attempts do not
// take this coarse lease, so configured per-source parallelism remains usable.
type ProviderFeedLease struct {
	lease *exclusiveLease
}

// AcquireProviderFeedLease waits for exclusive reconciliation ownership of one
// SeriesProvider. Waiting is context-aware; callers must Release the result.
func AcquireProviderFeedLease(ctx context.Context, providerID uuid.UUID) (*ProviderFeedLease, error) {
	lease, err := providerReconcileLeases.acquire(ctx, providerID)
	if err != nil {
		return nil, err
	}
	return &ProviderFeedLease{lease: lease}, nil
}

// Release returns the provider reconcile ownership. It is idempotent.
func (l *ProviderFeedLease) Release() {
	if l != nil {
		l.lease.Release()
	}
}

// ProviderChapterLease gives a reconcile exclusive ownership of a single
// ProviderChapter's address-bound resolver cache through commit or rollback.
type ProviderChapterLease struct {
	lease *exclusiveLease
}

// AcquireProviderChapterLease waits for exclusive ownership of one
// ProviderChapter's address and resolver cache.
func AcquireProviderChapterLease(ctx context.Context, providerChapterID uuid.UUID) (*ProviderChapterLease, error) {
	lease, err := providerChapterLeases.acquire(ctx, providerChapterID, providerChapterLeaseCapacity)
	if err != nil {
		return nil, err
	}
	return &ProviderChapterLease{lease: lease}, nil
}

// Release returns the ProviderChapter ownership. It is idempotent.
func (l *ProviderChapterLease) Release() {
	if l != nil {
		l.lease.Release()
	}
}

// ProviderChapterFetchLease gives a fetch shared ownership of one
// ProviderChapter's address and resolver cache. Fetches may contend through the
// database admission guard concurrently, while a reconcile waits for every
// admitted owner and prevents new owners until its transaction finishes.
type ProviderChapterFetchLease struct {
	lease *exclusiveLease
}

// AcquireProviderChapterFetchLease waits for shared fetch ownership of one
// ProviderChapter. Callers must hold it through page-link persistence and
// staging cleanup.
func AcquireProviderChapterFetchLease(ctx context.Context, providerChapterID uuid.UUID) (*ProviderChapterFetchLease, error) {
	lease, err := providerChapterLeases.acquire(ctx, providerChapterID, 1)
	if err != nil {
		return nil, err
	}
	return &ProviderChapterFetchLease{lease: lease}, nil
}

// Release returns the ProviderChapter fetch ownership. It is idempotent.
func (l *ProviderChapterFetchLease) Release() {
	if l != nil {
		l.lease.Release()
	}
}
