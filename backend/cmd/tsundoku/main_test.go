package main

import (
	"context"
	"errors"
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
