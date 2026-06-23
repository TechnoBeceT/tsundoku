// Package suwayomi_test — shared test helpers for ProcessManager unit tests.
//
// Uses the TestHelperProcess re-exec pattern so that tests run without a real
// JVM, /bin/sh, or any external binary. The test binary re-executes itself with
// -test.run=TestHelperProcess and a GO_TEST_HELPER_PROCESS env var that selects
// the behaviour to simulate.
package suwayomi_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// helperEnvKey is the env-var that selects which helper scenario to run.
const helperEnvKey = "GO_SUWAYOMI_TEST_HELPER"

// TestHelperProcess is not a real test — it is the subprocess that the injected
// fake commands re-exec into. go test runs it when GO_SUWAYOMI_TEST_HELPER is
// set; otherwise it exits immediately so it does not appear as a test failure.
func TestHelperProcess(t *testing.T) {
	scenario := os.Getenv(helperEnvKey)
	if scenario == "" {
		// Not a helper invocation — nothing to do.
		return
	}

	switch scenario {
	case "ready":
		// Emit the ready signal, then block until killed (simulates a healthy
		// Suwayomi that stays running).
		fmt.Println("You are running Javalin")
		time.Sleep(10 * time.Second)

	case "never_ready":
		// Never emit the ready signal — simulates a stuck startup.
		time.Sleep(10 * time.Second)

	default:
		fmt.Fprintf(os.Stderr, "unknown helper scenario: %q\n", scenario)
		os.Exit(1)
	}

	os.Exit(0)
}

// helperCmd builds a *exec.Cmd that re-execs the test binary under the named
// scenario. All original os.Args are forwarded so the test infrastructure
// (flags, working directory, etc.) is preserved; -test.run is overridden to
// point at TestHelperProcess only.
func helperCmd(ctx context.Context, scenario string, _ string, _ ...string) *exec.Cmd {
	// Re-exec the running test binary.
	args := []string{
		"-test.run=TestHelperProcess",
		"-test.v=false",
	}
	cmd := exec.CommandContext(ctx, os.Args[0], args...) //nolint:gosec
	cmd.Env = append(os.Environ(), helperEnvKey+"="+scenario)
	return cmd
}

// fakeReady is a CommandContextFunc that emits the Javalin ready signal.
func fakeReady(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return helperCmd(ctx, "ready", "")
}

// fakeNeverReady is a CommandContextFunc that never emits the ready signal.
func fakeNeverReady(ctx context.Context, _ string, _ ...string) *exec.Cmd {
	return helperCmd(ctx, "never_ready", "")
}

// ensure the CommandContextFunc type satisfies the seam expected by SetCommandContext.
var _ suwayomi.CommandContextFunc = fakeReady
