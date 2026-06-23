// Package suwayomi — test-only exports.
//
// This file is compiled only during `go test`; nothing in it is visible in the
// production binary. It exposes unexported fields and helpers so that
// process_test.go can drive ProcessManager without a real JVM.
package suwayomi

import (
	"context"
	"os/exec"
	"time"
)

// CommandContextFunc is the signature of the injectable command-construction
// function used by ProcessManager. Tests replace this with a fake.
type CommandContextFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// SetCommandContext replaces pm's command-construction function.
// Tests call this before Start to inject a fake command.
func SetCommandContext(pm *ProcessManager, fn CommandContextFunc) {
	pm.commandContext = fn
}

// FindJarFile exposes the unexported findJarFile helper for direct testing.
func FindJarFile(dir string) (string, error) {
	return findJarFile(dir)
}

// CleanTmpDir exposes the unexported cleanTmpDir helper for direct testing.
func CleanTmpDir(dir string, maxAge time.Duration) {
	cleanTmpDir(dir, maxAge)
}

// KillProcess sends SIGKILL to the process managed by pm without going through
// the Stop path. Used in tests to exercise Wait's cmd.Wait() branch.
func KillProcess(pm *ProcessManager) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.cmd != nil && pm.cmd.Process != nil {
		_ = pm.cmd.Process.Kill()
	}
}
