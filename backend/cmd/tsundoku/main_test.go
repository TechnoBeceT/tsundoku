package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
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

func (r shutdownRecorder) Close() error {
	*r.steps = append(*r.steps, r.step)
	return nil
}

func TestGracefulShutdownJoinsRuntimeRetryBeforeLauncherClose(t *testing.T) {
	var steps []string
	gracefulShutdown(
		shutdownRecorder{steps: &steps, step: "http"},
		shutdownRecorder{steps: &steps, step: "runtime"},
		shutdownRecorder{steps: &steps, step: "launcher"},
	)
	if want := []string{"http", "runtime", "launcher"}; !reflect.DeepEqual(steps, want) {
		t.Fatalf("shutdown order = %v, want %v", steps, want)
	}
}
