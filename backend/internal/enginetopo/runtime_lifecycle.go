package enginetopo

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrRuntimeConvergenceClosed reports that process shutdown has closed the
// shared convergence admission boundary. Durable callers leave their desired
// revision pending; detached best-effort callers simply stop launching work.
var ErrRuntimeConvergenceClosed = errors.New("runtime convergence is closed")

type runtimeConvergenceLifecycle struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	active int
	idle   chan struct{}
}

type runtimeConvergenceContextKey struct{}

func newRuntimeConvergenceLifecycle() *runtimeConvergenceLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &runtimeConvergenceLifecycle{ctx: ctx, cancel: cancel, idle: idle}
}

// RunRuntime admits one complete convergence operation. Calls nested through
// settings/source services reuse the outer admission via the context marker,
// allowing those services to keep their detached failure-metadata tails inside
// the same shutdown join without deadlocking on re-entry.
func (a *SourceRuntimeApplier) RunRuntime(ctx context.Context, run func(context.Context) error) error {
	if owner, _ := ctx.Value(runtimeConvergenceContextKey{}).(*runtimeConvergenceLifecycle); owner == a.admissions {
		return run(ctx)
	}
	opCtx, finish, err := a.admissions.admit(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return run(opCtx)
}

func (l *runtimeConvergenceLifecycle) admit(caller context.Context) (context.Context, func(), error) {
	if err := caller.Err(); err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, nil, ErrRuntimeConvergenceClosed
	}
	if l.active == 0 {
		l.idle = make(chan struct{})
	}
	l.active++
	l.mu.Unlock()

	opCtx, cancel := context.WithCancel(caller)
	stopLifecycleCancel := context.AfterFunc(l.ctx, cancel)
	opCtx = context.WithValue(opCtx, runtimeConvergenceContextKey{}, l)
	finish := func() {
		stopLifecycleCancel()
		cancel()
		l.mu.Lock()
		l.active--
		if l.active == 0 {
			close(l.idle)
		}
		l.mu.Unlock()
	}
	return opCtx, finish, nil
}

// ShutdownRuntimeConvergence atomically closes admission, cancels every active
// operation, and joins their complete outer calls. The mutex-owned active/idle
// generation avoids WaitGroup Add/Wait races; repeated calls may safely retry a
// timed-out join and calls after a completed shutdown return immediately.
func (a *SourceRuntimeApplier) ShutdownRuntimeConvergence(ctx context.Context) error {
	a.admissions.mu.Lock()
	a.admissions.closed = true
	a.admissions.cancel()
	idle := a.admissions.idle
	a.admissions.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("enginetopo.ShutdownRuntimeConvergence: %w", ctx.Err())
	}
}
