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
type runtimeSerializationContextKey struct{}

// runtimeScopeToken is a revocable capability carried by one admitted or
// serialized outer scope. The nested-use count closes the small race where a
// child starts just as its outer scope finishes: the child either enters while
// the token is live and is joined, or observes the revoked token and fails
// closed without executing through an expired scope.
type runtimeScopeToken struct {
	lifecycle *runtimeConvergenceLifecycle
	mu        sync.Mutex
	active    bool
	users     int
	idle      chan struct{}
}

func newRuntimeScopeToken(lifecycle *runtimeConvergenceLifecycle) *runtimeScopeToken {
	idle := make(chan struct{})
	close(idle)
	return &runtimeScopeToken{lifecycle: lifecycle, active: true, idle: idle}
}

func (t *runtimeScopeToken) enter() (func(), bool) {
	t.mu.Lock()
	if !t.active {
		t.mu.Unlock()
		return nil, false
	}
	t.lifecycle.mu.Lock()
	closed := t.lifecycle.closed
	t.lifecycle.mu.Unlock()
	if closed {
		t.mu.Unlock()
		return nil, false
	}
	if t.users == 0 {
		t.idle = make(chan struct{})
	}
	t.users++
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		t.users--
		if t.users == 0 {
			close(t.idle)
		}
		t.mu.Unlock()
	}, true
}

func (t *runtimeScopeToken) revokeAndJoin() {
	t.mu.Lock()
	t.active = false
	idle := t.idle
	t.mu.Unlock()
	<-idle
}

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
	if token, _ := ctx.Value(runtimeConvergenceContextKey{}).(*runtimeScopeToken); token != nil && token.lifecycle == a.admissions {
		leave, live := token.enter()
		if !live {
			return ErrRuntimeConvergenceClosed
		}
		defer leave()
		nestedCtx, cancel := context.WithCancel(ctx)
		stopLifecycleCancel := context.AfterFunc(a.admissions.ctx, cancel)
		defer stopLifecycleCancel()
		defer cancel()
		return run(nestedCtx)
	}
	opCtx, finish, err := a.admissions.admit(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return run(opCtx)
}

// RunSerializedRuntime admits and serializes one complete dependency-using
// convergence sequence. Calls made by that sequence through ReconcileNetwork,
// ReconcileRuntime, or ApplySourceRuntime reuse the held serializer, so the boot
// pipeline can be one atomic lifecycle without deadlocking on nested entry.
func (a *SourceRuntimeApplier) RunSerializedRuntime(ctx context.Context, run func(context.Context) error) error {
	return a.RunRuntime(ctx, func(ctx context.Context) error {
		if scope, _ := ctx.Value(runtimeSerializationContextKey{}).(*runtimeScopeToken); scope != nil && scope.lifecycle == a.admissions {
			leave, live := scope.enter()
			if !live {
				return ErrRuntimeConvergenceClosed
			}
			defer leave()
			return run(ctx)
		}
		if err := a.lifecycle.Acquire(ctx, 1); err != nil {
			return fmt.Errorf("enginetopo.SourceRuntimeApplier.RunSerializedRuntime: acquire lifecycle: %w", err)
		}
		defer a.lifecycle.Release(1)
		scope := newRuntimeScopeToken(a.admissions)
		defer scope.revokeAndJoin()
		return run(context.WithValue(ctx, runtimeSerializationContextKey{}, scope))
	})
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
	token := newRuntimeScopeToken(l)
	opCtx = context.WithValue(opCtx, runtimeConvergenceContextKey{}, token)
	finish := func() {
		stopLifecycleCancel()
		cancel()
		token.revokeAndJoin()
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
