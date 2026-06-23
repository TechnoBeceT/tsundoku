// Package suwayomi — lifecycle management for the embedded Suwayomi-Server
// process. This file covers launching the JAR, detecting the ready signal,
// and stopping the process cleanly.
package suwayomi

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/technobecet/tsundoku/internal/config"
)

// readySignal is the substring that appears in Suwayomi's stdout when the
// Javalin HTTP server has finished binding and is ready to accept requests.
const readySignal = "You are running Javalin"

// stopGracePeriod is how long Stop waits for a SIGTERM-ed process to exit
// before escalating to SIGKILL.
const stopGracePeriod = 5 * time.Second

// tmpDirMaxAge is the threshold beyond which files in the Suwayomi tmp
// directory are considered stale and removed on startup.
const tmpDirMaxAge = 60 * time.Minute

// ProcessManager manages the lifecycle of the embedded Suwayomi-Server process.
// It launches the JAR with the required JVM flags, blocks until the server is
// ready, and provides a graceful stop path.
//
// Zero value is not usable — construct with NewProcessManager.
type ProcessManager struct {
	cfg config.SuwayomiConfig

	// commandContext constructs the *exec.Cmd to run. Defaults to
	// exec.CommandContext; replaced by tests via export_test.go to inject a
	// fake process without a real JVM.
	commandContext func(ctx context.Context, name string, args ...string) *exec.Cmd

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool

	// waitDone is closed and waitErr is written by the single background waiter
	// goroutine that owns cmd.Wait(). Both Stop and the public Wait method select
	// on this channel instead of calling cmd.Wait() themselves, ensuring that
	// cmd.Wait() is called by exactly one goroutine (exec.Cmd documents Wait as
	// not safe to call concurrently or more than once).
	waitDone chan struct{}
	waitErr  error
}

// NewProcessManager returns a ProcessManager configured from cfg.
// The manager does not start Suwayomi until Start is called.
func NewProcessManager(cfg config.SuwayomiConfig) *ProcessManager {
	return &ProcessManager{
		cfg:            cfg,
		commandContext: exec.CommandContext,
	}
}

// Start provisions the Suwayomi JAR (via EnsureJAR), then launches it under a
// Java process and blocks until one of three outcomes:
//   - the ready signal ("You are running Javalin") appears on stdout → returns nil,
//     IsRunning() becomes true;
//   - cfg.StartTimeout elapses → Stop is called, returns a timeout error;
//   - ctx is cancelled → Stop is called, returns ctx.Err().
//
// Stdout and stderr are forwarded to the structured logger at debug level.
// Start is not safe to call concurrently or while the process is already running.
func (pm *ProcessManager) Start(ctx context.Context) error {
	jarPath, err := EnsureJAR(ctx, pm.cfg)
	if err != nil {
		return fmt.Errorf("suwayomi.ProcessManager.Start: provision JAR: %w", err)
	}

	readyCh, err := pm.launch(ctx, jarPath)
	if err != nil {
		return err
	}

	return pm.waitReady(ctx, readyCh)
}

// launch builds the java command, starts the subprocess, wires up the stderr
// forwarder and the stdout scanner goroutines, and returns a channel that is
// closed when the ready signal is detected. launch is extracted from Start to
// keep each function within the cyclomatic-complexity limit.
func (pm *ProcessManager) launch(ctx context.Context, jarPath string) (<-chan struct{}, error) {
	suwayomiDir := filepath.Join(pm.cfg.RuntimeDir, "Suwayomi")

	// Prepare the tmp directory and remove stale files from previous runs.
	tmpDir := filepath.Join(suwayomiDir, "tmp")
	if mkErr := os.MkdirAll(tmpDir, 0o750); mkErr != nil {
		slog.Warn("suwayomi: could not create tmp dir", "err", mkErr)
	} else {
		cleanTmpDir(tmpDir, tmpDirMaxAge)
	}

	// Remove Chrome singleton lock left by a previously un-gracefully-stopped run.
	_ = os.Remove(filepath.Join(suwayomiDir, "webview", "SingletonLock"))

	args := []string{
		fmt.Sprintf("-Dsuwayomi.tachidesk.config.server.rootDir=%s", pm.cfg.RuntimeDir),
		fmt.Sprintf("-Djava.io.tmpdir=%s", tmpDir),
		"-jar", jarPath,
	}

	slog.Info("suwayomi: starting process", "jar", jarPath)

	cmd := pm.commandContext(ctx, "java", args...)
	cmd.Dir = pm.cfg.RuntimeDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("suwayomi.ProcessManager.Start: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("suwayomi.ProcessManager.Start: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("suwayomi.ProcessManager.Start: exec: %w", err)
	}

	waitDone := make(chan struct{})
	pm.mu.Lock()
	pm.cmd = cmd
	pm.waitDone = waitDone
	pm.waitErr = nil
	pm.mu.Unlock()

	// Single background waiter — the only goroutine that calls cmd.Wait().
	// exec.Cmd documents Wait as not safe to call concurrently or more than once;
	// all other code (Stop, public Wait) blocks on waitDone instead.
	go func() {
		err := cmd.Wait()
		pm.mu.Lock()
		pm.waitErr = err
		pm.mu.Unlock()
		close(waitDone)
	}()

	// Forward stderr to the logger; no synchronisation needed — this goroutine
	// only reads from the pipe and calls the logger.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			slog.Debug("suwayomi stderr", "line", scanner.Text())
		}
	}()

	// Scan stdout for the ready signal. readyCh is closed when the signal is
	// found; the goroutine continues draining stdout to prevent pipe buffer stalls
	// which would block the Java process.
	readyCh := make(chan struct{})
	go func() {
		signalled := false
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			slog.Debug("suwayomi stdout", "line", line)
			if !signalled && strings.Contains(line, readySignal) {
				signalled = true
				close(readyCh)
				// Continue draining to avoid stalling the Java process.
			}
		}
	}()

	return readyCh, nil
}

// waitReady blocks until readyCh is closed (ready), the start timeout elapses,
// or ctx is cancelled. On timeout or cancellation, Stop is called before
// returning the error. ctx must be the same context passed to Start.
func (pm *ProcessManager) waitReady(ctx context.Context, readyCh <-chan struct{}) error {
	timeout := pm.cfg.StartTimeout
	if timeout <= 0 {
		// Defensive path: StartTimeout is validated by config.Load; this guard
		// ensures the process never hangs indefinitely if a zero value slips through.
		timeout = 2 * time.Minute
	}

	select {
	case <-readyCh:
		pm.mu.Lock()
		pm.running = true
		pm.mu.Unlock()
		slog.Info("suwayomi: ready")
		return nil

	case <-time.After(timeout):
		pm.Stop()
		return fmt.Errorf("suwayomi.ProcessManager.Start: did not become ready within %s", timeout)

	case <-ctx.Done():
		pm.Stop()
		return ctx.Err()
	}
}

// Stop sends SIGTERM to the running process and waits up to stopGracePeriod for
// it to exit cleanly. If the grace period elapses, SIGKILL is sent. Stop is
// idempotent — calling it on an already-stopped manager is a no-op.
//
// Stop is safe to call from any goroutine while Start is blocked in its ready
// wait; when the process exits its stdout/stderr pipes close, so the stdout
// scan goroutine terminates naturally and waitReady falls through to its
// timeout/ctx arm.
func (pm *ProcessManager) Stop() {
	pm.mu.Lock()

	if pm.cmd == nil || pm.cmd.Process == nil {
		pm.mu.Unlock()
		return
	}

	slog.Info("suwayomi: stopping process")

	// Capture both cmd and waitDone under the lock. The background waiter goroutine
	// owns cmd.Wait(); Stop and the public Wait method select on waitDone instead of
	// calling cmd.Wait() directly, so there is no concurrent cmd.Wait() race.
	cmd := pm.cmd
	waitDone := pm.waitDone

	pm.mu.Unlock()

	// os.Interrupt = SIGTERM on Unix — request a graceful JVM shutdown.
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		// Defensive path: SIGTERM to a live process fails only on OS-level errors
		// (e.g. the process exited between the nil-check and Signal). Unreachable in
		// normal operation; coverage gap is expected per engineering standard.
		slog.Warn("suwayomi: SIGTERM failed, killing", "err", err)
		_ = cmd.Process.Kill()
		<-waitDone
		pm.mu.Lock()
		pm.running = false
		pm.cmd = nil
		pm.mu.Unlock()
		return
	}

	// Wait for the background waiter to observe process exit, with escalation to SIGKILL.
	select {
	case <-waitDone:
		slog.Info("suwayomi: stopped gracefully")
	case <-time.After(stopGracePeriod):
		// Defensive path: a real JVM that ignores SIGTERM for >5 s is pathological.
		// Triggering this in a unit test would require a slow-exit fake that holds
		// for the full grace period, making the test suite unacceptably slow.
		// Coverage gap documented per engineering standard.
		slog.Warn("suwayomi: grace period elapsed, killing")
		_ = cmd.Process.Kill()
		<-waitDone
	}

	pm.mu.Lock()
	pm.running = false
	pm.cmd = nil
	pm.mu.Unlock()
}

// IsRunning reports whether the Suwayomi process is currently running and ready.
// It returns true only after Start has returned nil and before Stop is called.
func (pm *ProcessManager) IsRunning() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.running
}

// Wait blocks until the underlying process exits and returns its exit error, if
// any. It returns nil immediately if no process has been started.
//
// Callers that need to observe the exit status of a running Suwayomi (e.g. to
// detect unexpected crashes) should call Wait after Start returns nil.
func (pm *ProcessManager) Wait() error {
	pm.mu.Lock()
	waitDone := pm.waitDone
	pm.mu.Unlock()

	if waitDone == nil {
		return nil
	}

	<-waitDone

	pm.mu.Lock()
	err := pm.waitErr
	pm.mu.Unlock()
	return err
}

// findJarFile searches dir for the first regular file whose name ends in ".jar"
// (case-insensitive). It returns the absolute path to that file, or an error if
// no such file exists or dir cannot be read.
func findJarFile(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("suwayomi.findJarFile: read %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".jar") {
			return filepath.Join(dir, entry.Name()), nil
		}
	}

	return "", fmt.Errorf("suwayomi.findJarFile: no JAR file found in %s", dir)
}

// cleanTmpDir removes entries in dir whose modification time is older than
// maxAge. Errors from individual removals are silently discarded — a stale tmp
// file is cosmetic; it must not block startup.
func cleanTmpDir(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				_ = os.RemoveAll(path)
			} else {
				_ = os.Remove(path)
			}
		}
	}
}
