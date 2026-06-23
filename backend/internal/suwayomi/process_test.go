// Package suwayomi_test — unit tests for ProcessManager.
//
// No real JVM is required. Tests substitute the command-construction function
// via the export_test.go seam, pointing the manager at a shell one-liner that
// simulates the two outcomes: ready (prints the signal) and never-ready
// (sleeps forever and is killed by timeout/stop).
package suwayomi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// shortCfg returns a SuwayomiConfig with a tiny StartTimeout suitable for
// deterministic timeout tests. RuntimeDir is set to a temp directory that
// already contains a dummy JAR so Start can find one without EnsureJAR hitting
// the network.
func shortCfg(t *testing.T, timeout time.Duration) (config.SuwayomiConfig, string) {
	t.Helper()

	dir := t.TempDir()
	suwayomiDir := filepath.Join(dir, "Suwayomi")
	if err := os.MkdirAll(suwayomiDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Place a dummy JAR whose name matches JARFileName("v0.0.0-test") so that
	// EnsureJAR's idempotency stat-check fires and no download is attempted.
	jarPath := filepath.Join(suwayomiDir, "Suwayomi-Server-v0.0.0-test.jar")
	if err := os.WriteFile(jarPath, []byte("fake"), 0o600); err != nil {
		t.Fatalf("write dummy jar: %v", err)
	}

	cfg := config.SuwayomiConfig{
		RuntimeDir:   dir,
		Version:      "v0.0.0-test",
		StartTimeout: timeout,
		// DownloadURLTemplate left empty: EnsureJAR's stat check finds the
		// pre-seeded JAR by its deterministic name and returns before any
		// network request is attempted.
	}
	return cfg, jarPath
}

// TestProcessManager_StartReady verifies that a fake process which emits the
// ready signal causes Start to return nil and sets IsRunning to true.
func TestProcessManager_StartReady(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 5*time.Second)
	pm := suwayomi.NewProcessManager(cfg)

	// Inject a fake command: print the ready signal then sleep so the process
	// stays alive long enough for IsRunning to be asserted.
	suwayomi.SetCommandContext(pm, fakeReady)

	ctx := context.Background()
	if err := pm.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	if !pm.IsRunning() {
		t.Fatal("IsRunning should be true after Start returns nil")
	}

	// Cleanup: stop so the fake process does not linger after the test.
	pm.Stop()
}

// TestProcessManager_StartTimeout verifies that when the fake process never
// emits the ready signal, Start returns an error after StartTimeout, the
// process is stopped, and IsRunning is false.
func TestProcessManager_StartTimeout(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 200*time.Millisecond)
	pm := suwayomi.NewProcessManager(cfg)

	// Inject a fake command that never prints the ready signal.
	suwayomi.SetCommandContext(pm, fakeNeverReady)

	ctx := context.Background()
	err := pm.Start(ctx)
	if err == nil {
		t.Fatal("Start: expected error on timeout, got nil")
	}
	if pm.IsRunning() {
		t.Fatal("IsRunning should be false after a timeout")
	}
}

// TestProcessManager_Stop verifies that Stop terminates a running fake process
// and flips IsRunning to false, and that calling Stop a second time is safe
// (idempotent).
func TestProcessManager_Stop(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 5*time.Second)
	pm := suwayomi.NewProcessManager(cfg)
	suwayomi.SetCommandContext(pm, fakeReady)

	ctx := context.Background()
	if err := pm.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !pm.IsRunning() {
		t.Fatal("expected IsRunning true after Start")
	}

	pm.Stop()
	if pm.IsRunning() {
		t.Fatal("IsRunning should be false after Stop")
	}

	// Second Stop must not panic or block.
	pm.Stop()
}

// TestProcessManager_CtxCancelled verifies that cancelling the context mid-start
// causes Start to return ctx.Err() (or a wrapping error that unwraps to it) and
// leaves IsRunning false.
func TestProcessManager_CtxCancelled(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 5*time.Second)
	pm := suwayomi.NewProcessManager(cfg)

	// Use a fake that never emits the ready signal so Start will be blocked
	// waiting when we cancel the context.
	suwayomi.SetCommandContext(pm, fakeNeverReady)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay so Start is already in its select loop.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := pm.Start(ctx)
	if err == nil {
		t.Fatal("Start: expected error on ctx cancel, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start: expected context.Canceled in error chain, got: %v", err)
	}
	if pm.IsRunning() {
		t.Fatal("IsRunning should be false after ctx cancel")
	}
}

// TestFindJarFile_Found verifies that findJarFile returns the path to a .jar
// file placed in the search directory.
func TestFindJarFile_Found(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := filepath.Join(dir, "Test-Server-v1.0.jar")
	if err := os.WriteFile(want, []byte("fake"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := suwayomi.FindJarFile(dir)
	if err != nil {
		t.Fatalf("FindJarFile: %v", err)
	}
	if got != want {
		t.Fatalf("FindJarFile: got %q, want %q", got, want)
	}
}

// TestFindJarFile_NotFound verifies that findJarFile returns an error when no
// .jar file is present.
func TestFindJarFile_NotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Write a non-JAR file to confirm the predicate is selective.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := suwayomi.FindJarFile(dir)
	if err == nil {
		t.Fatal("FindJarFile: expected error when no .jar present, got nil")
	}
}

// TestFindJarFile_EmptyDir verifies that findJarFile errors on an empty directory.
func TestFindJarFile_EmptyDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := suwayomi.FindJarFile(dir)
	if err == nil {
		t.Fatal("FindJarFile: expected error for empty dir, got nil")
	}
}

// TestFindJarFile_UnreadableDir verifies that findJarFile returns an error when
// the directory cannot be read (permissions).
func TestFindJarFile_UnreadableDir(t *testing.T) {
	t.Parallel()

	if os.Getuid() == 0 {
		t.Skip("running as root: permission check is ineffective")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o600) }) //nolint:gosec // restoring dir perms for cleanup

	_, err := suwayomi.FindJarFile(dir)
	if err == nil {
		t.Fatal("FindJarFile: expected error for unreadable dir, got nil")
	}
}

// TestProcessManager_Wait_NilCmd verifies that Wait returns nil when no process
// has been started.
func TestProcessManager_Wait_NilCmd(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 5*time.Second)
	pm := suwayomi.NewProcessManager(cfg)

	if err := pm.Wait(); err != nil {
		t.Fatalf("Wait on unstarted manager: expected nil, got %v", err)
	}
}

// TestProcessManager_Wait_AfterStop verifies that Wait returns nil (or an exit
// error) after the process has been stopped.
func TestProcessManager_Wait_AfterStop(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 5*time.Second)
	pm := suwayomi.NewProcessManager(cfg)
	suwayomi.SetCommandContext(pm, fakeReady)

	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pm.Stop()

	// Wait must not block indefinitely after Stop has already called cmd.Wait.
	done := make(chan error, 1)
	go func() { done <- pm.Wait() }()

	select {
	case err := <-done:
		// A nil or "signal: interrupt" exit error are both acceptable here
		// because Stop already waited for the process; cmd.Wait after that
		// returns an already-consumed error (varies by OS). Both are valid.
		_ = err
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return within 3 seconds after Stop")
	}
}

// TestCleanTmpDir removes only entries older than maxAge and leaves recent
// ones untouched, including stale subdirectories.
func TestCleanTmpDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	past := time.Now().Add(-2 * time.Hour)

	// Create a stale file by writing it and then backdating its mtime.
	stale := filepath.Join(dir, "stale.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := os.Chtimes(stale, past, past); err != nil {
		t.Fatalf("chtimes stale file: %v", err)
	}

	// Create a stale subdirectory.
	staleDir := filepath.Join(dir, "stale-subdir")
	if err := os.Mkdir(staleDir, 0o700); err != nil {
		t.Fatalf("mkdir stale subdir: %v", err)
	}
	if err := os.Chtimes(staleDir, past, past); err != nil {
		t.Fatalf("chtimes stale dir: %v", err)
	}

	// Create a fresh file that must not be removed.
	fresh := filepath.Join(dir, "fresh.tmp")
	if err := os.WriteFile(fresh, []byte("y"), 0o600); err != nil {
		t.Fatalf("write fresh: %v", err)
	}

	suwayomi.CleanTmpDir(dir, time.Hour)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("CleanTmpDir: stale file should have been removed")
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Error("CleanTmpDir: stale directory should have been removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("CleanTmpDir: fresh file should still exist: %v", err)
	}
}

// TestCleanTmpDir_UnreadableDir verifies that cleanTmpDir is a no-op and does
// not panic when the directory cannot be read.
func TestCleanTmpDir_UnreadableDir(t *testing.T) {
	t.Parallel()

	if os.Getuid() == 0 {
		t.Skip("running as root: permission check is ineffective")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o600) }) //nolint:gosec // restoring dir perms for cleanup

	// Must not panic; silently returns when dir is unreadable.
	suwayomi.CleanTmpDir(dir, time.Hour)
}

// TestProcessManager_Wait_WithRunningProcess verifies that Wait blocks until
// the running process exits and then returns (possibly an exit error).
func TestProcessManager_Wait_WithRunningProcess(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 5*time.Second)
	pm := suwayomi.NewProcessManager(cfg)
	suwayomi.SetCommandContext(pm, fakeReady)

	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Send SIGTERM directly so the process exits while pm.cmd is still non-nil
	// (before Stop clears it). This exercises the cmd.Wait() branch of Wait.
	suwayomi.KillProcess(pm)

	done := make(chan error, 1)
	go func() { done <- pm.Wait() }()

	select {
	case <-done:
		// any error (nil or exit error) is acceptable — the branch is covered.
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return within 3 seconds after process killed")
	}
}

// TestProcessManager_StopWait_Concurrent verifies that calling Stop and Wait
// concurrently from separate goroutines does not trigger the race detector. This
// locks in the fix for the unlocked pm.cmd dereference inside Stop's goroutine:
// cmd is now captured under pm.mu before the goroutine is launched so that both
// Stop's internal goroutine and an external Wait caller operate on their own
// locally captured *exec.Cmd reference.
func TestProcessManager_StopWait_Concurrent(t *testing.T) {
	t.Parallel()

	cfg, _ := shortCfg(t, 5*time.Second)
	pm := suwayomi.NewProcessManager(cfg)
	suwayomi.SetCommandContext(pm, fakeReady)

	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Fire Stop and Wait concurrently a few times to give the race detector
	// sufficient opportunity to observe any unsynchronised access.
	const concurrency = 5
	ready := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(concurrency * 2)

	for range concurrency {
		go func() {
			defer wg.Done()
			<-ready
			pm.Stop()
		}()
		go func() {
			defer wg.Done()
			<-ready
			_ = pm.Wait()
		}()
	}

	close(ready) // release all goroutines simultaneously
	wg.Wait()
}

// TestProcessManager_Start_EnsureJARFails verifies that Start returns an error
// when EnsureJAR fails (JAR absent and no download URL configured).
func TestProcessManager_Start_EnsureJARFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// RuntimeDir without a Suwayomi/ subdirectory or JAR, and no DownloadURLTemplate —
	// EnsureJAR will attempt to build a GET request and fail with a bad URL.
	cfg := config.SuwayomiConfig{
		RuntimeDir:          dir,
		Version:             "v0.0.0-bad",
		StartTimeout:        5 * time.Second,
		DownloadURLTemplate: "://bad-url", // deliberately malformed
	}
	pm := suwayomi.NewProcessManager(cfg)

	err := pm.Start(context.Background())
	if err == nil {
		t.Fatal("Start: expected error when EnsureJAR fails, got nil")
	}
	if pm.IsRunning() {
		t.Fatal("IsRunning should be false after EnsureJAR error")
	}
}
