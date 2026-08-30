package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginetopo"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

type runtimeRestoreFunc func(context.Context) error

func (f runtimeRestoreFunc) ReconcileRuntime(ctx context.Context) error { return f(ctx) }

type pendingRuntimeFunc func(context.Context) error

func (f pendingRuntimeFunc) ReconcilePending(ctx context.Context) error { return f(ctx) }

func TestRunSourceRuntimeReconcileRetriesGlobalAndSourceIntents(t *testing.T) {
	var restoreCalls, globalCalls, sourceCalls int
	runSourceRuntimeReconcile(
		context.Background(),
		runtimeRestoreFunc(func(context.Context) error {
			restoreCalls++
			return errors.New("restore unavailable")
		}),
		pendingRuntimeFunc(func(context.Context) error {
			globalCalls++
			return errors.New("global unavailable")
		}),
		pendingRuntimeFunc(func(context.Context) error {
			sourceCalls++
			return errors.New("source unavailable")
		}),
	)
	if restoreCalls != 1 || globalCalls != 1 || sourceCalls != 1 {
		t.Fatalf("startup calls restore/global/source = %d/%d/%d, want 1/1/1 despite independent failures", restoreCalls, globalCalls, sourceCalls)
	}
}

func TestRuntimePendingGroupAttemptsBothDomainsAfterFailure(t *testing.T) {
	var globalCalls, sourceCalls int
	group := runtimePendingGroup{
		global: pendingRuntimeFunc(func(context.Context) error {
			globalCalls++
			return errors.New("global unavailable")
		}),
		sources: pendingRuntimeFunc(func(context.Context) error {
			sourceCalls++
			return errors.New("source unavailable")
		}),
	}
	if err := group.ReconcilePending(context.Background()); err == nil {
		t.Fatal("ReconcilePending error = nil, want joined failures")
	}
	if globalCalls != 1 || sourceCalls != 1 {
		t.Fatalf("runtime pending calls global/source = %d/%d, want 1/1", globalCalls, sourceCalls)
	}
}

type shutdownRecorder struct {
	steps *[]string
	step  string
}

func (r shutdownRecorder) Shutdown(context.Context) error {
	*r.steps = append(*r.steps, r.step)
	return nil
}

func (r shutdownRecorder) ShutdownRuntimeRetry(context.Context) error {
	*r.steps = append(*r.steps, r.step)
	return nil
}

func (r shutdownRecorder) ShutdownRuntimeConvergence(context.Context) error {
	*r.steps = append(*r.steps, r.step)
	return nil
}

func (r shutdownRecorder) Close() error {
	*r.steps = append(*r.steps, r.step)
	return nil
}

func TestGracefulShutdownJoinsConvergenceAndRuntimeRetryBeforeLauncherClose(t *testing.T) {
	var steps []string
	gracefulShutdown(
		shutdownRecorder{steps: &steps, step: "http"},
		shutdownRecorder{steps: &steps, step: "convergence"},
		shutdownRecorder{steps: &steps, step: "runtime"},
		shutdownRecorder{steps: &steps, step: "launcher"},
	)
	if want := []string{"http", "convergence", "runtime", "launcher"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("shutdown order = %v, want %v", steps, want)
	}
}

type timedOutServer struct{}

func (timedOutServer) Shutdown(context.Context) error { return context.DeadlineExceeded }

type blockingConvergenceShutdown struct {
	entered chan struct{}
	release chan struct{}
}

func (s blockingConvergenceShutdown) ShutdownRuntimeConvergence(context.Context) error {
	close(s.entered)
	<-s.release
	return nil
}

type closeSignal struct{ closed chan struct{} }

func (s closeSignal) Close() error {
	close(s.closed)
	return nil
}

func TestGracefulShutdownAfterHTTPDrainTimeoutStillJoinsConvergenceBeforeClose(t *testing.T) {
	convergenceEntered := make(chan struct{})
	releaseConvergence := make(chan struct{})
	launcherClosed := make(chan struct{})
	done := make(chan struct{})
	go func() {
		gracefulShutdown(
			timedOutServer{},
			blockingConvergenceShutdown{entered: convergenceEntered, release: releaseConvergence},
			shutdownRecorder{steps: new([]string), step: "runtime"},
			closeSignal{closed: launcherClosed},
		)
		close(done)
	}()
	select {
	case <-convergenceEntered:
	case <-time.After(time.Second):
		t.Fatal("graceful shutdown did not close convergence admissions after HTTP timeout")
	}
	select {
	case <-launcherClosed:
		t.Fatal("launcher closed before active convergence joined")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseConvergence)
	select {
	case <-launcherClosed:
	case <-time.After(time.Second):
		t.Fatal("launcher did not close after convergence joined")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("graceful shutdown did not finish")
	}
}

func TestTrackedDetachedRuntimeCallbacksJoinStartupAndNetworkTails(t *testing.T) {
	for _, name := range []string{"startup", "network"} {
		t.Run(name, func(t *testing.T) {
			coordinator := enginetopo.NewSourceRuntimeApplier(sourceenginefake.New(), enginetopo.NetworkReconcileDeps{})
			entered := make(chan struct{})
			tailEntered := make(chan struct{})
			releaseTail := make(chan struct{})
			callbackDone := make(chan struct{})
			go func() {
				runTrackedRuntimeConvergence(context.Background(), coordinator, func(ctx context.Context) {
					close(entered)
					<-ctx.Done()
					close(tailEntered)
					<-releaseTail
				})
				close(callbackDone)
			}()
			<-entered

			shutdownDone := make(chan error, 1)
			go func() { shutdownDone <- coordinator.ShutdownRuntimeConvergence(context.Background()) }()
			select {
			case <-tailEntered:
			case <-time.After(time.Second):
				t.Fatalf("%s callback did not receive convergence cancellation", name)
			}
			select {
			case err := <-shutdownDone:
				t.Fatalf("shutdown returned before %s callback tail: %v", name, err)
			case <-time.After(100 * time.Millisecond):
			}
			close(releaseTail)
			select {
			case <-callbackDone:
			case <-time.After(time.Second):
				t.Fatalf("%s callback did not finish", name)
			}
			select {
			case err := <-shutdownDone:
				if err != nil {
					t.Fatalf("ShutdownRuntimeConvergence: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatalf("shutdown did not join %s callback", name)
			}
		})
	}
}
