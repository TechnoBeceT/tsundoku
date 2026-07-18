package enginehost_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

// newTestLauncher builds a Launcher with the given fakes and short timers so the
// lifecycle paths run fast and deterministically.
func newTestLauncher(t *testing.T, cfg enginehost.EngineHostLauncherConfig, starter enginehost.ProcessStarter, prober enginehost.HealthProber, opts ...enginehost.Option) (*enginehost.Launcher, *recordingFactory) {
	t.Helper()
	// spawn now links a shared extensions dir under DataDir before starting the
	// process (see linkSharedExtensions), so DataDir must be a WRITABLE root.
	// Tests that don't care about the exact path pass an empty DataDir and get a
	// per-test temp dir here.
	if cfg.DataDir == "" {
		cfg.DataDir = t.TempDir()
	}
	rf := &recordingFactory{}
	base := []enginehost.Option{
		enginehost.WithStarter(starter),
		enginehost.WithHealthProber(prober),
		enginehost.WithPortAllocator(fixedPortAllocator(41001)),
		enginehost.WithStartTimeout(50 * time.Millisecond),
		enginehost.WithPollInterval(2 * time.Millisecond),
		enginehost.WithStopGrace(20 * time.Millisecond),
	}
	l := enginehost.New(cfg, rf.factory(), append(base, opts...)...)
	return l, rf
}

// TestEnsureProfile_SpawnsWithAllocatedPortAndDataDir pins the core spawn: the
// instance listens on the ALLOCATED port, its data dir is the per-profile
// "<DataDir>/profiles/<hash>", and the returned Instance carries the profile key
// + a factory-built client aimed at the instance's base URL.
func TestEnsureProfile_SpawnsWithAllocatedPortAndDataDir(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	root := t.TempDir()
	cfg := enginehost.EngineHostLauncherConfig{HostBin: "/bin/host", DataDir: root}
	l, rf := newTestLauncher(t, cfg, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001)))

	inst, err := l.EnsureProfile(context.Background(), profile("socks-1|global|"))
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	if inst.Key != "socks-1|global|" {
		t.Errorf("Instance.Key = %q, want the profile key", inst.Key)
	}
	if inst.BaseURL != "http://127.0.0.1:41001" {
		t.Errorf("Instance.BaseURL = %q, want http://127.0.0.1:41001", inst.BaseURL)
	}
	if inst.Client == nil {
		t.Error("Instance.Client is nil — factory not invoked")
	}
	if got := len(rf.urls); got != 1 || rf.urls[0] != "http://127.0.0.1:41001" {
		t.Errorf("factory urls = %v, want one entry for the base URL", rf.urls)
	}

	if starter.callCount() != 1 {
		t.Fatalf("starter called %d times, want 1", starter.callCount())
	}
	call := starter.lastCall()
	if call.port != 41001 {
		t.Errorf("spawn port = %d, want 41001 (the allocated port)", call.port)
	}
	wantDir := enginehost.DataDirFor(root, "socks-1|global|")
	if call.dataDir != wantDir {
		t.Errorf("spawn dataDir = %q, want %q (per-profile dir)", call.dataDir, wantDir)
	}
}

// TestEnsureProfile_IdempotentReuse proves a second EnsureProfile for a running,
// healthy profile returns the cached instance WITHOUT a second spawn.
func TestEnsureProfile_IdempotentReuse(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber)

	first, err := l.EnsureProfile(context.Background(), profile("k1"))
	if err != nil {
		t.Fatalf("EnsureProfile #1: %v", err)
	}
	second, err := l.EnsureProfile(context.Background(), profile("k1"))
	if err != nil {
		t.Fatalf("EnsureProfile #2: %v", err)
	}

	if starter.callCount() != 1 {
		t.Fatalf("starter called %d times, want 1 (idempotent reuse)", starter.callCount())
	}
	if first.BaseURL != second.BaseURL {
		t.Errorf("reuse returned a different instance: %q vs %q", first.BaseURL, second.BaseURL)
	}
}

// TestEnsureProfile_DeadCachedRespawns proves a cached instance whose process has
// exited is discarded and respawned.
func TestEnsureProfile_DeadCachedRespawns(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile #1: %v", err)
	}
	// Simulate the JVM dying.
	starter.proc(0).exit()

	inst, err := l.EnsureProfile(context.Background(), profile("k1"))
	if err != nil {
		t.Fatalf("EnsureProfile #2: %v", err)
	}
	if starter.callCount() != 2 {
		t.Fatalf("starter called %d times, want 2 (respawn after death)", starter.callCount())
	}
	if inst.BaseURL != "http://127.0.0.1:41002" {
		t.Errorf("respawn BaseURL = %q, want the second port", inst.BaseURL)
	}
}

// TestEnsureProfile_UnhealthyCachedRespawns proves a cached instance that is
// alive but no longer answers /health is torn down and respawned. The prober
// fails ONLY on the liveness check (call #2), so the respawn succeeds.
func TestEnsureProfile_UnhealthyCachedRespawns(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	prober := sequenceProber(nil, errors.New("wedged"), nil)
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile #1: %v", err)
	}
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile #2: %v", err)
	}

	if starter.callCount() != 2 {
		t.Fatalf("starter called %d times, want 2 (respawn after unhealthy)", starter.callCount())
	}
	// The wedged first instance must have been signalled to stop.
	if !starter.proc(0).wasSignalled() {
		t.Error("wedged instance was not stopped before respawn")
	}
}

// TestEnsureProfile_ExtensionsLinkFailureDegrades proves the shared-extensions
// link is NOT best-effort: when it can't be created (here DataDir is a regular
// FILE, so "<DataDir>/extensions" can't be made) the spawn ABORTS with an error
// BEFORE the process is started, so ReconcileNetwork degrades the profile to the
// default instance rather than running a source-less one.
func TestEnsureProfile_ExtensionsLinkFailureDegrades(t *testing.T) {
	root := t.TempDir()
	fileAsDataDir := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(fileAsDataDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	starter := &fakeStarter{}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DataDir: fileAsDataDir}, starter, okProber)

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err == nil {
		t.Fatal("EnsureProfile succeeded, want an extensions-link error (degrade to default)")
	}
	if starter.callCount() != 0 {
		t.Errorf("process started %d times despite the link failure, want 0 (abort before spawn)", starter.callCount())
	}
}

// TestEnsureProfile_HealthTimeoutKillsAndErrors proves a spawn whose /health
// never comes up is killed and surfaces an error (so the caller degrades the
// profile to the default instance).
func TestEnsureProfile_HealthTimeoutKillsAndErrors(t *testing.T) {
	starter := &fakeStarter{}
	prober := func(string) error { return errors.New("never ready") }
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober)

	inst, err := l.EnsureProfile(context.Background(), profile("k1"))
	if err == nil {
		t.Fatalf("EnsureProfile succeeded, want a not-ready error; inst=%+v", inst)
	}
	if !starter.proc(0).wasKilled() {
		t.Error("timed-out spawn was not killed")
	}
}

// TestEnsureProfile_ProcessExitsBeforeReady proves a spawn whose process crashes
// during startup errors promptly (not only on the startup-timeout).
func TestEnsureProfile_ProcessExitsBeforeReady(t *testing.T) {
	starter := &fakeStarter{}
	prober := func(string) error { return errors.New("not up") }
	// A long start timeout so the test can only pass via the early-exit branch.
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithStartTimeout(10*time.Second))

	// Kill the process right after it is created so awaitReady observes the exit.
	go func() {
		for starter.callCount() == 0 {
			time.Sleep(time.Millisecond)
		}
		starter.proc(0).exit()
	}()

	start := time.Now()
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err == nil {
		t.Fatal("EnsureProfile succeeded, want an exited-before-ready error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("EnsureProfile took %s — did not short-circuit on process exit", elapsed)
	}
}

// TestEnsureProfile_CtxCancelDuringWait proves a cancelled ctx aborts the
// readiness wait (a shutdown mid-spawn returns promptly instead of blocking the
// full startup timeout).
func TestEnsureProfile_CtxCancelDuringWait(t *testing.T) {
	starter := &fakeStarter{}
	prober := func(string) error { return errors.New("not up") }
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithStartTimeout(10*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for starter.callCount() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	if _, err := l.EnsureProfile(ctx, profile("k1")); err == nil {
		t.Fatal("EnsureProfile succeeded, want a ctx-cancelled error")
	}
	if !starter.proc(0).wasKilled() {
		t.Error("cancelled spawn was not killed")
	}
}

// TestEnsureProfile_StarterErrorSurfaces proves a spawn whose process fails to
// start returns an error (degrading the profile to the default instance).
func TestEnsureProfile_StarterErrorSurfaces(t *testing.T) {
	starter := &fakeStarter{err: errors.New("exec failed")}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber)

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err == nil {
		t.Fatal("EnsureProfile succeeded, want a start error")
	}
}

// TestEnsureProfile_PortAllocErrorSurfaces proves a free-port allocation failure
// aborts the spawn with an error before any process is started.
func TestEnsureProfile_PortAllocErrorSurfaces(t *testing.T) {
	starter := &fakeStarter{}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithPortAllocator(func() (int, error) { return 0, errors.New("no ports") }))

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err == nil {
		t.Fatal("EnsureProfile succeeded, want a port-allocation error")
	}
	if starter.callCount() != 0 {
		t.Errorf("starter called %d times despite port-alloc failure, want 0", starter.callCount())
	}
}

// TestRetire_StopsNonKeptKeepsKept proves Retire stops the instances absent from
// keep and leaves the kept one running (reused without a respawn).
func TestRetire_StopsNonKeptKeepsKept(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile k1: %v", err)
	}
	if _, err := l.EnsureProfile(context.Background(), profile("k2")); err != nil {
		t.Fatalf("EnsureProfile k2: %v", err)
	}

	l.Retire(context.Background(), map[string]bool{"k2": true})

	if !starter.proc(0).wasSignalled() {
		t.Error("k1 (not kept) was not stopped")
	}
	if starter.proc(1).wasSignalled() {
		t.Error("k2 (kept) was wrongly stopped")
	}

	// k2 is still cached: EnsureProfile reuses it (no third spawn); k1 respawns.
	if _, err := l.EnsureProfile(context.Background(), profile("k2")); err != nil {
		t.Fatalf("EnsureProfile k2 after retire: %v", err)
	}
	if starter.callCount() != 2 {
		t.Errorf("starter called %d times, want 2 (k2 reused, not respawned)", starter.callCount())
	}
}

// TestRetire_EmptyOnEmptyIsNoOp pins the zero-disruption invariant: Retire with
// an empty keep-set on a launcher that spawned nothing is a safe no-op.
func TestRetire_EmptyOnEmptyIsNoOp(t *testing.T) {
	starter := &fakeStarter{}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber)

	l.Retire(context.Background(), map[string]bool{})

	if starter.callCount() != 0 {
		t.Errorf("starter called %d times on a no-op Retire, want 0", starter.callCount())
	}
}

// TestClose_StopsAllAndRefusesFurther proves Close stops every instance and that
// EnsureProfile afterwards is refused.
func TestClose_StopsAllAndRefusesFurther(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile k1: %v", err)
	}
	if _, err := l.EnsureProfile(context.Background(), profile("k2")); err != nil {
		t.Fatalf("EnsureProfile k2: %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !starter.proc(0).wasSignalled() || !starter.proc(1).wasSignalled() {
		t.Error("Close did not stop all instances")
	}

	if _, err := l.EnsureProfile(context.Background(), profile("k3")); !errors.Is(err, enginehost.ErrLauncherClosed) {
		t.Errorf("EnsureProfile after Close = %v, want ErrLauncherClosed", err)
	}
}

// TestClose_Idempotent proves Close can be called twice safely.
func TestClose_Idempotent(t *testing.T) {
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, &fakeStarter{}, okProber)
	if err := l.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close #2: %v", err)
	}
}

// TestStopInstance_KillEscalation proves a process that ignores SIGTERM is
// SIGKILLed after the grace period.
func TestStopInstance_KillEscalation(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false} // ignores SIGTERM
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStopGrace(10*time.Millisecond))

	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	done := make(chan struct{})
	go func() { l.Retire(context.Background(), map[string]bool{}); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Retire blocked — kill escalation did not fire")
	}

	if !starter.proc(0).wasKilled() {
		t.Error("SIGTERM-ignoring process was not SIGKILLed after the grace period")
	}
}
