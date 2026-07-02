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
// Java process and blocks until one of two outcomes:
//   - the ready signal ("You are running Javalin") appears on stdout → returns nil,
//     IsRunning() becomes true;
//   - ctx is cancelled → Stop is called, returns ctx.Err().
//
// cfg.StartTimeout is a SOFT WARN THRESHOLD, not a kill deadline: if it elapses
// before the ready signal, Start logs one loud warning and keeps waiting (a long
// startup is most likely a schema migration — killing it mid-migration is the
// corruption this milestone avoids). Tickers therefore start when ready fires,
// however late.
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
	}
	// Optionally point the embedded JVM at an explicit DB engine (Postgres) —
	// empty when DatabaseType is blank (Suwayomi's default H2). All -D props
	// must precede -jar.
	args = append(args, databaseArgs(pm.cfg)...)
	args = append(args, "-jar", jarPath)

	// Use the configured java executable (defaults to "java" on PATH).
	// Override via cfg.JavaPath when the system default JVM is too old
	// (Suwayomi v2.2.2100 requires Java 21+).
	javaExec := pm.cfg.JavaPath
	if javaExec == "" {
		javaExec = "java"
	}

	slog.Info("suwayomi: starting process", "jar", jarPath, "java", javaExec)

	cmd := pm.commandContext(ctx, javaExec, args...)
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

// databaseArgs returns the JVM -D system properties that point the embedded
// Suwayomi JVM at an explicit DB engine, or nil when DatabaseType is blank
// (Suwayomi's default H2 — unchanged behaviour). Keeping it a small pure helper
// makes the DB-selection logic unit-testable without launching a JVM.
//
// Keys CONFIRMED against Suwayomi v2.2.2100 server-reference.conf
// (server.databaseType / .databaseUrl / .databaseUsername / .databasePassword);
// the JVM override prefix is "suwayomi.tachidesk.config." — the same prefix the
// rootDir/tmpdir args already use. The DatabaseURL is the bare postgresql://
// form: Suwayomi prepends "jdbc:" itself (DBManager.createHikariDataSource).
//
// The password is part of the returned args (the JVM needs it) and is never
// logged — launch() logs only the jar path and java executable. NOTE the
// residual: because it is passed as a JVM -D argument it is visible in the
// process command line (ps / /proc/<pid>/cmdline) to local users. Acceptable
// under the single-owner homelab threat model; writing it to server.conf
// instead is a deferred hardening.
func databaseArgs(cfg config.SuwayomiConfig) []string {
	if cfg.DatabaseType == "" {
		return nil
	}
	return []string{
		fmt.Sprintf("-Dsuwayomi.tachidesk.config.server.databaseType=%s", cfg.DatabaseType),
		fmt.Sprintf("-Dsuwayomi.tachidesk.config.server.databaseUrl=%s", cfg.DatabaseURL),
		fmt.Sprintf("-Dsuwayomi.tachidesk.config.server.databaseUsername=%s", cfg.DatabaseUsername),
		fmt.Sprintf("-Dsuwayomi.tachidesk.config.server.databasePassword=%s", cfg.DatabasePassword),
	}
}

// waitReady blocks until one of three outcomes:
//   - readyCh is closed (the ready signal appeared) → returns nil;
//   - the process exits before becoming ready (a boot crash, e.g. bad Postgres
//     credentials/host) → returns an error (the process is already gone, so it is
//     NOT killed); this surfaces the failure instead of blocking forever;
//   - ctx is cancelled (a deliberate shutdown) → calls Stop and returns ctx.Err().
//
// StartTimeout is a SOFT WARN THRESHOLD, not a kill deadline: when it elapses the
// process is NOT killed — waitReady logs one loud warning and keeps waiting for
// the ready signal. A long startup is almost always an H2/Postgres schema
// migration, and killing the JVM mid-migration is the exact DB-corruption mode
// this milestone eliminates (H2 auto-commits DDL, so a SIGKILL leaves a
// half-applied schema). ctx must be the same context passed to Start.
func (pm *ProcessManager) waitReady(ctx context.Context, readyCh <-chan struct{}) error {
	timeout := pm.cfg.StartTimeout
	if timeout <= 0 {
		// Defensive path: StartTimeout is validated by config.Load; this guard
		// keeps the warn threshold sane if a zero value ever slips through.
		timeout = 2 * time.Minute
	}

	// Capture the waiter channel before the loop (mirrors Stop). It is closed by
	// the background waiter goroutine in launch() when cmd.Wait() returns, so this
	// arm observes a process that exits before signalling ready.
	pm.mu.Lock()
	waitDone := pm.waitDone
	pm.mu.Unlock()

	// Single-shot timer: fires once to emit the warn, then never again — the
	// loop falls through to wait on the other arms without a deadline.
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-readyCh:
			pm.markReady()
			return nil

		case <-waitDone:
			// Process exited before the ready signal — a boot crash (e.g. bad
			// Postgres credentials/host/port). Do NOT kill (already gone); surface
			// it so Start's caller logs the error and tickers never start silently.
			return pm.exitBeforeReadyErr()

		case <-timer.C:
			slog.Warn("suwayomi: startup exceeding threshold — a long H2/Postgres "+
				"schema migration is the likely cause; NOT killing the process, "+
				"continuing to wait (downloads suspended until ready)",
				"threshold", timeout)
			// Keep waiting: re-enter the select on readyCh/waitDone/ctx only.

		case <-ctx.Done():
			pm.Stop()
			return ctx.Err()
		}
	}
}

// markReady flips the manager into the running state and logs readiness. It is
// extracted from waitReady so the ready path stays a single statement inside the
// select loop.
func (pm *ProcessManager) markReady() {
	pm.mu.Lock()
	pm.running = true
	pm.mu.Unlock()
	slog.Info("suwayomi: ready")
}

// exitBeforeReadyErr builds the error returned when the process exits before
// signalling ready. It reads the recorded exit error under the mutex and wraps
// it; a nil exit error (a clean exit-0 before the ready signal) is still a
// startup failure, so it gets an explicit message rather than a wrapped nil.
func (pm *ProcessManager) exitBeforeReadyErr() error {
	pm.mu.Lock()
	exitErr := pm.waitErr
	pm.mu.Unlock()

	const msg = "suwayomi.ProcessManager.Start: process exited before becoming ready"
	if exitErr == nil {
		return fmt.Errorf("%s (clean exit before ready signal)", msg)
	}
	return fmt.Errorf("%s: %w", msg, exitErr)
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
