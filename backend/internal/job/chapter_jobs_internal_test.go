package job

import (
	"context"
	"testing"
)

type panicOnceRuntimeReconciler struct{ calls int }

func (r *panicOnceRuntimeReconciler) ReconcilePending(context.Context) error {
	r.calls++
	if r.calls == 1 {
		panic("unexpected")
	}
	return nil
}

func TestRuntimeRetryPassContainsPanicAndRemainsReusable(t *testing.T) {
	reconciler := &panicOnceRuntimeReconciler{}
	runner := NewRunner(nil, nil, nil, "", nil)
	runner.SetRuntimeReconciler(reconciler)
	if err := runner.runRuntimeRetryPass(context.Background()); err == nil {
		t.Fatal("panic returned nil error")
	}
	if err := runner.runRuntimeRetryPass(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if reconciler.calls != 2 {
		t.Fatalf("calls = %d, want 2", reconciler.calls)
	}
}

func TestCanceledTriggeredDownloadLoopRequeuesOpportunity(t *testing.T) {
	r := NewRunner(nil, nil, nil, "", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r.runDownloadCycleLogging(ctx, true)

	select {
	case <-r.trigger:
	default:
		t.Fatal("canceled loop consumed the shared download trigger instead of returning it for a live loop")
	}
}
