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

	"github.com/technobecet/tsundoku/internal/config"
)

// CommandContextFunc is the signature of the injectable command-construction
// function used by ProcessManager. Tests replace this with a fake.
type CommandContextFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// SetCommandContext replaces pm's command-construction function.
// Tests call this before Start to inject a fake command.
func SetCommandContext(pm *ProcessManager, fn CommandContextFunc) {
	pm.commandContext = fn
}

// CleanTmpDir exposes the unexported cleanTmpDir helper for direct testing.
func CleanTmpDir(dir string, maxAge time.Duration) {
	cleanTmpDir(dir, maxAge)
}

// DatabaseArgs exposes the unexported databaseArgs helper so black-box tests can
// assert the embedded-Suwayomi DB -D system properties without launching a JVM.
func DatabaseArgs(cfg config.SuwayomiConfig) []string {
	return databaseArgs(cfg)
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

// ValidateImagePage exposes the unexported validateImagePage guard so black-box
// tests can pin the decode/content/empty checks with real image bytes.
func ValidateImagePage(data []byte) error {
	return validateImagePage(data)
}

// NewChapterCacheClock builds a ChapterCache with an injectable TTL provider AND
// an injectable clock, so black-box tests can drive both TTL expiry and TTL hot
// reload deterministically (advance now / mutate the provider instead of
// sleeping). Production uses NewChapterCache (clock = time.Now).
func NewChapterCacheClock(ttl func(context.Context) time.Duration, now func() time.Time) *ChapterCache {
	c := NewChapterCache(ttl)
	c.now = now
	return c
}
