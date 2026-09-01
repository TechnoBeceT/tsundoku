package enginehost_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginehost"
	"github.com/technobecet/tsundoku/internal/engineroute"
)

// fixedInterval is a supervise-interval accessor returning a constant (the
// superviseOnce-driven tests never consult it, but NewSupervisor requires one).
func fixedInterval(d time.Duration) func(context.Context) time.Duration {
	return func(context.Context) time.Duration { return d }
}

func exhaustedStatus(sequence int64, queued int, sources ...enginehost.EngineSourceStatus) enginehost.EngineStatus {
	return enginehost.EngineStatus{
		Ready:               true,
		SourceWorkers:       8,
		PerSourceLimit:      2,
		Queued:              queued,
		Running:             8,
		CompletionSequence:  sequence,
		OldestRunningMillis: 180001,
		BusiestSources:      append([]enginehost.EngineSourceStatus(nil), sources...),
		KCEF:                enginehost.KCEFStatus{State: enginehost.KCEFStateReady},
	}
}

func TestSupervise_KCEFCapabilityLossRestartsOnlyAffectedProfile(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rerouter := newFakeRerouter()
	var lossAvailable atomic.Bool
	status := func(_ context.Context, baseURL string) (enginehost.EngineStatus, error) {
		if baseURL == "http://127.0.0.1:41001" && lossAvailable.CompareAndSwap(true, false) {
			return enginehost.EngineStatus{KCEF: enginehost.KCEFStatus{
				State: enginehost.KCEFStateFailed, ErrorCode: kcefError(enginehost.KCEFErrorInitFailed),
			}}, nil
		}
		return readyKCEFStatus(), nil
	}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rerouter), enginehost.WithStatusProber(status),
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	s := enginehost.NewSupervisor(l, fixedInterval(time.Second))
	p1 := engineroute.Profile{Key: "failed-kcef", SourceIDs: []int64{11}, KCEFEnabled: true}
	p2 := engineroute.Profile{Key: "ready-kcef", SourceIDs: []int64{22}, KCEFEnabled: true}
	if _, err := l.EnsureProfile(context.Background(), p1); err != nil {
		t.Fatalf("EnsureProfile p1: %v", err)
	}
	if _, err := l.EnsureProfile(context.Background(), p2); err != nil {
		t.Fatalf("EnsureProfile p2: %v", err)
	}
	lossAvailable.Store(true)

	enginehost.SuperviseOnce(s, context.Background(), time.Now())

	if got := starter.callCount(); got != 3 {
		t.Fatalf("starts = %d, want only failed profile restarted", got)
	}
	if !starter.proc(0).wasSignalled() {
		t.Fatal("failed profile process was not stopped before restart")
	}
	if starter.proc(1).wasSignalled() {
		t.Fatal("ready profile process was disturbed by another profile's KCEF failure")
	}
	if rerouter.isDegraded(11) || rerouter.isDegraded(22) {
		t.Fatal("successful restart must restore only the affected profile without degrading peers")
	}
}

var exhaustedSources = []enginehost.EngineSourceStatus{
	{SourceID: 11, Running: 2},
	{SourceID: 22, Running: 2},
	{SourceID: 33, Running: 2},
	{SourceID: 44, Running: 2},
}

func ensureManagedProfile(t *testing.T, l *enginehost.Launcher, key string, sourceIDs ...int64) {
	t.Helper()
	p := profile(key)
	if len(sourceIDs) > 0 {
		p = profileWithSources(key, sourceIDs...)
	}
	if _, err := l.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
}

func changingQueueStatusProber(sample *atomic.Int32) enginehost.StatusProber {
	return func(context.Context, string) (enginehost.EngineStatus, error) {
		n := int(sample.Add(1))
		sources := append([]enginehost.EngineSourceStatus(nil), exhaustedSources...)
		if n%2 == 0 {
			sources[0], sources[3] = sources[3], sources[0]
		}
		for i := range sources {
			sources[i].Queued = n + i
		}
		return exhaustedStatus(41, n*7, sources...), nil
	}
}

func checkingDiagnosticSink(t *testing.T, starter *fakeStarter, diagnostics *atomic.Int32) func(context.Context, enginehost.ExhaustionDiagnostic) {
	t.Helper()
	return func(_ context.Context, d enginehost.ExhaustionDiagnostic) {
		if starter.callCount() != 1 {
			t.Errorf("diagnostic captured after restart: starter calls = %d, want 1", starter.callCount())
		}
		if d.ProfileKey != "k1" || d.PID != 1 || d.Status.Queued == 0 {
			t.Errorf("diagnostic = %+v, want bounded profile/pid/status evidence", d)
		}
		diagnostics.Add(1)
	}
}

func TestSupervise_StableExhaustionRestartsOnceAfterSixSamples(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	var sample atomic.Int32
	var diagnostics atomic.Int32
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(changingQueueStatusProber(&sample)),
		enginehost.WithExhaustionDiagnosticSink(checkingDiagnosticSink(t, starter, &diagnostics)))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	ensureManagedProfile(t, l, "k1", 10)

	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts after five samples = %d, want 1", got)
	}
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(5*30*time.Second))
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts after sixth stable sample = %d, want 2", got)
	}
	if got := diagnostics.Load(); got != 1 {
		t.Errorf("diagnostics = %d, want one bundle before restart", got)
	}
}

func TestSupervise_ExhaustionEvidenceRequiresThirtySecondCadence(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return exhaustedStatus(41, 0, exhaustedSources...), nil
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(5*time.Second))
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 6; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*5*time.Second))
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts after six five-second observations = %d, want 1", got)
	}

	for _, elapsed := range []time.Duration{30, 60, 90, 120} {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(elapsed*time.Second))
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts before the 150-second sixth proof = %d, want 1", got)
	}
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(150*time.Second))
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts at the exact 150-second sixth proof = %d, want 2", got)
	}
}

func TestSupervise_ExhaustionEvidenceCannotCatchUpWithClusteredObservations(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return exhaustedStatus(41, 0, exhaustedSources...), nil
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(5*time.Second))
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	now := time.Unix(1_700_000_000, 0)
	for _, elapsed := range []time.Duration{0, 100, 101, 102, 120, 150} {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(elapsed*time.Second))
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts after clustered observations = %d, want 1", got)
	}
	for _, elapsed := range []time.Duration{180, 210} {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(elapsed*time.Second))
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts before six spaced observations = %d, want 1", got)
	}
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(240*time.Second))
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts after six spaced observations = %d, want 2", got)
	}
}

func TestSupervise_CompletionAndPhysicalFingerprintChangesResetEvidence(t *testing.T) {
	tests := []struct {
		name    string
		changed enginehost.EngineStatus
	}{
		{name: "completion sequence", changed: exhaustedStatus(42, 0, exhaustedSources...)},
		{name: "physical running sources", changed: exhaustedStatus(41, 0,
			enginehost.EngineSourceStatus{SourceID: 11, Running: 2},
			enginehost.EngineSourceStatus{SourceID: 22, Running: 2},
			enginehost.EngineSourceStatus{SourceID: 33, Running: 2},
			enginehost.EngineSourceStatus{SourceID: 55, Running: 2})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { testEvidenceChangeResetsProof(t, tt.changed) })
	}
}

func testEvidenceChangeResetsProof(t *testing.T, changed enginehost.EngineStatus) {
	t.Helper()
	starter := &fakeStarter{closeOnSignal: true}
	var mu sync.Mutex
	current := exhaustedStatus(41, 0, exhaustedSources...)
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			mu.Lock()
			defer mu.Unlock()
			return current, nil
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	ensureManagedProfile(t, l, "k1")
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	mu.Lock()
	current = changed
	mu.Unlock()
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(5*30*time.Second))
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts on changed sixth sample = %d, want 1", got)
	}
	for i := 0; i < 4; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(6+i)*30*time.Second))
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts after five samples of new evidence = %d, want 1", got)
	}
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(10*30*time.Second))
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts after sixth sample of new evidence = %d, want 2", got)
	}
}

func TestSupervise_UnprovenStatusResetsEvidenceFailSafe(t *testing.T) {
	tests := []struct {
		name   string
		status enginehost.EngineStatus
		err    error
	}{
		{name: "progressing age", status: func() enginehost.EngineStatus {
			s := exhaustedStatus(41, 0, exhaustedSources...)
			s.OldestRunningMillis = 180000
			return s
		}()},
		{name: "non-full", status: func() enginehost.EngineStatus {
			s := exhaustedStatus(41, 0, exhaustedSources...)
			s.Running = 7
			s.BusiestSources[3].Running = 1
			return s
		}()},
		{name: "malformed typed status", status: func() enginehost.EngineStatus {
			s := exhaustedStatus(41, 0, exhaustedSources...)
			s.BusiestSources = nil
			return s
		}()},
		{name: "status failure", err: errors.New("status unavailable")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			starter := &fakeStarter{closeOnSignal: true}
			var calls atomic.Int32
			l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
				enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
					if calls.Add(1) == 6 {
						return tt.status, tt.err
					}
					return exhaustedStatus(41, 0, exhaustedSources...), nil
				}))
			sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
			if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
				t.Fatalf("EnsureProfile: %v", err)
			}
			now := time.Unix(1_700_000_000, 0)
			for i := 0; i < 7; i++ {
				enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
			}
			if got := starter.callCount(); got != 1 {
				t.Fatalf("starts = %d, want 1 after evidence reset", got)
			}
		})
	}
}

func TestSupervise_ExhaustionCooldownSuppressesRestartLoop(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return exhaustedStatus(41, 0, exhaustedSources...), nil
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 6; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts after first recovery = %d, want 2", got)
	}
	for i := 6; i < 20; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts during ten-minute cooldown = %d, want 2", got)
	}
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(10*time.Minute+5*30*time.Second))
	if got := starter.callCount(); got != 3 {
		t.Fatalf("starts after cooldown with stable evidence = %d, want 3", got)
	}
}

func TestSupervise_InstanceReplacementCannotRestartStaleTarget(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	var l *enginehost.Launcher
	var calls atomic.Int32
	statusProber := func(ctx context.Context, _ string) (enginehost.EngineStatus, error) {
		if calls.Add(1) == 6 {
			l.Retire(ctx, map[string]bool{})
			if _, err := l.EnsureProfile(ctx, profile("k1")); err != nil {
				t.Errorf("replace instance: %v", err)
			}
		}
		return exhaustedStatus(41, 0, exhaustedSources...), nil
	}
	l, _ = newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(statusProber))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 6; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts = %d, want initial + explicit replacement only", got)
	}
}

func TestSupervise_CancelledContextCannotRestartExhaustedInstance(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	ctx, cancel := context.WithCancel(context.Background())
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(func(probeCtx context.Context, _ string) (enginehost.EngineStatus, error) {
			cancel()
			<-probeCtx.Done()
			return enginehost.EngineStatus{}, probeCtx.Err()
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	enginehost.SuperviseOnce(sup, ctx, time.Now())
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts after cancellation = %d, want 1", got)
	}
}

func TestSupervise_HealthDownPathDoesNotConsultStatus(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	var statusCalls atomic.Int32
	prober := sequenceProber(nil, errors.New("health down"), nil)
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			statusCalls.Add(1)
			return enginehost.EngineStatus{}, errors.New("must not be called")
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	if _, err := l.EnsureProfile(context.Background(), profile("k1")); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	enginehost.SuperviseOnce(sup, context.Background(), time.Now())
	if got := starter.callCount(); got != 2 {
		t.Fatalf("health-down starts = %d, want existing immediate restart", got)
	}
	if got := statusCalls.Load(); got != 0 {
		t.Errorf("status calls while health down = %d, want 0", got)
	}
}

func toggledHealthProber(healthDown *atomic.Bool) func(string) error {
	return func(string) error {
		if healthDown.Load() {
			return errors.New("health down")
		}
		return nil
	}
}

func assertResetEvidenceWithCooldown(t *testing.T, l *enginehost.Launcher, wantEligible time.Time) {
	t.Helper()
	evidence, ok := enginehost.InstanceExhaustionEvidence(l, "k1")
	if !ok {
		t.Fatal("instance k1 not managed")
	}
	if evidence.Consecutive != 0 || evidence.Fingerprint != "" ||
		!evidence.FirstSampleAt.IsZero() || !evidence.NextSampleAt.IsZero() {
		t.Errorf("evidence after health-down = %+v, want reset proof", evidence)
	}
	if !evidence.NextEligibleAt.Equal(wantEligible) {
		t.Errorf("cooldown eligibility = %s, want preserved %s", evidence.NextEligibleAt, wantEligible)
	}
}

func TestSupervise_HealthDownDuringCooldownInvalidatesExhaustionProof(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	var healthDown atomic.Bool
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, toggledHealthProber(&healthDown),
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return exhaustedStatus(41, 0, exhaustedSources...), nil
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	ensureManagedProfile(t, l, "k1")

	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 6; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts after first exhaustion recovery = %d, want 2", got)
	}
	for i := 6; i < 11; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	healthDown.Store(true)
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(11*30*time.Second))
	healthDown.Store(false)

	wantEligible := now.Add(5*30*time.Second + 10*time.Minute)
	assertResetEvidenceWithCooldown(t, l, wantEligible)
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts during cooldown health-down = %d, want 2", got)
	}

	enginehost.SuperviseOnce(sup, context.Background(), wantEligible)
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts from stale proof at cooldown boundary = %d, want 2", got)
	}
	if evidence, _ := enginehost.InstanceExhaustionEvidence(l, "k1"); evidence.Consecutive != 1 {
		t.Errorf("evidence after first post-down sample = %+v, want a fresh one-sample proof", evidence)
	}
}

func cancellingStatusProber(armed *atomic.Bool, cancel *context.CancelFunc) enginehost.StatusProber {
	return func(context.Context, string) (enginehost.EngineStatus, error) {
		if armed.Swap(false) {
			(*cancel)()
		}
		return exhaustedStatus(41, 0, exhaustedSources...), nil
	}
}

func TestSupervise_CancellationAfterStatusInvalidatesPartialProof(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	var cancelOnProbe atomic.Bool
	var cancel context.CancelFunc
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(cancellingStatusProber(&cancelOnProbe, &cancel)))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	ensureManagedProfile(t, l, "k1")

	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}
	ctx, cancelPass := context.WithCancel(context.Background())
	cancel = cancelPass
	cancelOnProbe.Store(true)
	enginehost.SuperviseOnce(sup, ctx, now.Add(5*30*time.Second))

	evidence, ok := enginehost.InstanceExhaustionEvidence(l, "k1")
	if !ok || evidence.Consecutive != 0 || evidence.Fingerprint != "" || !evidence.FirstSampleAt.IsZero() {
		t.Fatalf("evidence after status cancellation = %+v ok=%v, want reset proof", evidence, ok)
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts after status cancellation = %d, want 1", got)
	}

	enginehost.SuperviseOnce(sup, context.Background(), now.Add(6*30*time.Second))
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts from stale proof after cancellation = %d, want 1", got)
	}
	if evidence, _ := enginehost.InstanceExhaustionEvidence(l, "k1"); evidence.Consecutive != 1 {
		t.Errorf("evidence after fresh context = %+v, want a fresh one-sample proof", evidence)
	}
}

func TestSupervise_CancellationDuringHealthPreventsProcessMutation(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	var cancelOnHealth atomic.Bool
	var cancel context.CancelFunc
	prober := func(string) error {
		if cancelOnHealth.Swap(false) {
			cancel()
			return errors.New("cancelled health probe")
		}
		return nil
	}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithRerouter(rr),
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return exhaustedStatus(41, 0, exhaustedSources...), nil
		}))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 5; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Duration(i)*30*time.Second))
	}

	ctx, cancelPass := context.WithCancel(context.Background())
	cancel = cancelPass
	cancelOnHealth.Store(true)
	enginehost.SuperviseOnce(sup, ctx, now.Add(5*30*time.Second))

	if got := starter.attemptCount(); got != 1 {
		t.Fatalf("start attempts after health cancellation = %d, want 1", got)
	}
	if starter.proc(0).wasSignalled() {
		t.Error("process was signalled after health cancellation")
	}
	if rr.isDegraded(10) {
		t.Error("source was degraded after health cancellation")
	}
	if evidence, ok := enginehost.InstanceExhaustionEvidence(l, "k1"); !ok || evidence.Consecutive != 0 || evidence.Fingerprint != "" {
		t.Errorf("evidence after health cancellation = %+v ok=%v, want reset proof", evidence, ok)
	}
}

// TestSupervise_RestartsDeadInstanceAndRestores proves the core supervision loop:
// a managed instance that has died is degraded to the default engine, restarted
// on its existing port via the launcher's spawn path, and — once the restart is
// healthy — its sources are restored to their own instance. Recovery happens in
// the same pass as the successful restart.
func TestSupervise_RestartsDeadInstanceAndRestores(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10, 11)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	// The JVM dies.
	starter.proc(0).exit()

	enginehost.SuperviseOnce(sup, context.Background(), time.Now())

	if starter.callCount() != 2 {
		t.Fatalf("starter callCount = %d, want 2 (initial spawn + one restart)", starter.callCount())
	}
	if rr.isDegraded(10) || rr.isDegraded(11) {
		t.Error("sources still degraded after a successful restart, want restored")
	}
	degraded, failures, ok := enginehost.InstanceSupervisionState(l, "k1")
	if !ok {
		t.Fatal("instance k1 not managed after restart")
	}
	if degraded || failures != 0 {
		t.Errorf("post-recovery state degraded=%v failures=%d, want false/0", degraded, failures)
	}
	// The restart reused the SAME port + data dir (so the base route stays valid).
	if got := starter.lastCall().port; got != 41001 {
		t.Errorf("restart port = %d, want 41001 (reused, not reallocated)", got)
	}
}

// TestSupervise_RestartFailsToCapThenStaysDegraded proves a persistently-crashing
// instance is retried up to the cap (bounded attempts), its sources stay degraded
// to the default engine, and after the cap the supervisor stops attempting
// (enters cooldown) rather than hammering in a tight loop.
func TestSupervise_RestartFailsToCapThenStaysDegraded(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		enginehost.WithSuperviseMaxRestarts(3),
		enginehost.WithSuperviseBackoff(time.Millisecond, time.Millisecond),
		enginehost.WithSuperviseCooldown(time.Hour))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	// Make every subsequent restart fail, then kill the instance.
	starter.setErr(errors.New("crash on boot"))
	starter.proc(0).exit()

	now := time.Now()
	// Drive enough passes to exhaust the cap and enter cooldown. Advance now past
	// the (tiny) backoff each pass so backoff never masks an attempt.
	for i := 0; i < 5; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now)
		now = now.Add(time.Second)
	}

	// Exactly maxRestarts (3) restart attempts on top of the initial spawn: passes
	// 4 and 5 hit the cap/cooldown and attempt nothing (no tight loop).
	if got := starter.attemptCount(); got != 1+3 {
		t.Errorf("Start attempts = %d, want 4 (initial + 3 capped restarts)", got)
	}
	if !rr.isDegraded(10) {
		t.Error("source not degraded while its instance is persistently down")
	}
	degraded, failures, ok := enginehost.InstanceSupervisionState(l, "k1")
	if !ok || !degraded {
		t.Errorf("state ok=%v degraded=%v, want managed + degraded", ok, degraded)
	}
	// After the cap the failure count is reset for the cooldown episode.
	if failures != 0 {
		t.Errorf("post-cap failures = %d, want 0 (reset into cooldown)", failures)
	}
}

// TestSupervise_RecoversAfterAFailedRestart proves a failed restart arms backoff
// and keeps the sources degraded, and that a LATER pass (once the environment
// recovers) restarts the instance and restores routing.
func TestSupervise_RecoversAfterAFailedRestart(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		enginehost.WithSuperviseBackoff(time.Millisecond, time.Millisecond))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.setErr(errors.New("transient"))
	starter.proc(0).exit()

	now := time.Now()
	enginehost.SuperviseOnce(sup, context.Background(), now) // fails → degraded, backoff armed
	if !rr.isDegraded(10) {
		t.Fatal("source not degraded after the first failed restart")
	}
	if _, failures, _ := enginehost.InstanceSupervisionState(l, "k1"); failures != 1 {
		t.Errorf("failures after one failed restart = %d, want 1", failures)
	}

	// Environment recovers; a later pass (past the backoff) restarts + restores.
	starter.setErr(nil)
	enginehost.SuperviseOnce(sup, context.Background(), now.Add(time.Second))

	if rr.isDegraded(10) {
		t.Error("source still degraded after a successful recovery restart")
	}
	if degraded, failures, _ := enginehost.InstanceSupervisionState(l, "k1"); degraded || failures != 0 {
		t.Errorf("post-recovery state degraded=%v failures=%d, want false/0", degraded, failures)
	}
}

// TestSupervise_HealthyInstanceUntouched proves a healthy managed instance is
// left completely alone — never restarted, never degraded — and that a launcher
// managing nothing (the default-instance-only deployment: the default 7777 is
// owned by the entrypoint and never enters the launcher's map) is a pure no-op.
func TestSupervise_HealthyInstanceUntouched(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))

	// No instances yet: a pass over an empty launcher does nothing.
	enginehost.SuperviseOnce(sup, context.Background(), time.Now())
	if starter.attemptCount() != 0 {
		t.Fatalf("empty-launcher pass started %d processes, want 0", starter.attemptCount())
	}

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	enginehost.SuperviseOnce(sup, context.Background(), time.Now())

	if starter.callCount() != 1 {
		t.Errorf("healthy instance was restarted (%d spawns), want 1 (untouched)", starter.callCount())
	}
	if d, _ := rr.counts(); d != 0 {
		t.Errorf("healthy instance was degraded %d times, want 0", d)
	}
	if rr.isDegraded(10) {
		t.Error("healthy instance's source is degraded, want routed to its own instance")
	}
}

// TestSupervise_BackoffPreventsTightLoop proves a down instance is retried at most
// ONCE per pass while its backoff is unexpired: repeated passes at the same wall
// clock (within the backoff window) make no further restart attempts, so a
// persistently-failing instance is never hammered in a tight loop.
func TestSupervise_BackoffPreventsTightLoop(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		// A large backoff so the second+ passes at the same clock are all gated.
		enginehost.WithSuperviseBackoff(time.Hour, time.Hour))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.setErr(errors.New("still crashing"))
	starter.proc(0).exit()

	now := time.Now()
	for i := 0; i < 4; i++ {
		enginehost.SuperviseOnce(sup, context.Background(), now) // same clock every pass
	}

	// Initial spawn + exactly ONE restart attempt; the other three passes were
	// gated by the unexpired backoff.
	if got := starter.attemptCount(); got != 1+1 {
		t.Errorf("Start attempts = %d, want 2 (initial + one; backoff gated the rest)", got)
	}
	if !rr.isDegraded(10) {
		t.Error("source not degraded while its instance stays down")
	}
}

// TestSupervise_StartLoopRunsPasses proves the background Start loop actually
// drives supervision passes on its interval: a dead instance is auto-restarted
// without any manual SuperviseOnce, and the loop exits on ctx cancel.
func TestSupervise_StartLoopRunsPasses(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(2*time.Millisecond))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.proc(0).exit() // the instance dies before the loop starts

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.Start(ctx)

	// The loop should restart it (a second spawn) within a short window.
	deadline := time.After(2 * time.Second)
	for starter.callCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("Start loop did not restart the dead instance (callCount=%d)", starter.callCount())
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel() // loop must return on ctx cancel
}

// TestSupervise_RestartsWedgedInstanceStopsOldProcessFirst proves the restart
// lifecycle handles a WEDGED-but-alive instance (its process is still running but
// /health fails): the supervisor stops the old process BEFORE respawning on the
// same port, so the fresh process is not blocked by the old one still holding the
// port (which would otherwise fail every restart forever and leak the wedged JVM).
func TestSupervise_RestartsWedgedInstanceStopsOldProcessFirst(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true} // the wedged proc exits on SIGTERM
	rr := newFakeRerouter()
	// Healthy for the initial spawn, wedged (proc alive + /health failing) for the
	// supervise probe, then healthy again so the restart's own readiness gate passes.
	prober := sequenceProber(nil, errors.New("wedged: /health not answering"), nil)
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	// The instance is now wedged: its process stays ALIVE but /health fails.
	enginehost.SuperviseOnce(sup, context.Background(), time.Now())

	// The old (wedged) process was stopped before the respawn — without that, its
	// still-held port would block the fresh process every time.
	if !starter.proc(0).wasSignalled() {
		t.Error("wedged instance's old process was not stopped before restart")
	}
	// A fresh process was started, reusing the SAME port (no bind-failure loop).
	if starter.callCount() != 2 {
		t.Fatalf("starter callCount = %d, want 2 (initial spawn + one restart)", starter.callCount())
	}
	if got := starter.lastCall().port; got != 41001 {
		t.Errorf("restart port = %d, want 41001 (reused, not reallocated)", got)
	}
	// The restart brought it back: routing restored, counters reset.
	if rr.isDegraded(10) {
		t.Error("source still degraded after the wedged instance was restarted healthy")
	}
	if degraded, failures, ok := enginehost.InstanceSupervisionState(l, "k1"); !ok || degraded || failures != 0 {
		t.Errorf("post-restart state ok=%v degraded=%v failures=%d, want managed/false/0", ok, degraded, failures)
	}
}

// TestSupervise_SlowCrasherBoundedToCapThenDegraded proves the max-restart cap
// bounds a slow-crasher: an instance that passes /health on every restart and then
// dies shortly after must NOT be restarted every interval forever. Because the cap
// is measured over a rolling window counting every restart ATTEMPT (not a
// consecutive-failure count reset by each brief health-pass), the profile is
// bounded to ~the cap within the window and then stays degraded for the cooldown.
func TestSupervise_SlowCrasherBoundedToCapThenDegraded(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(30*time.Second),
		enginehost.WithSuperviseMaxRestarts(3),
		enginehost.WithSuperviseBackoff(time.Millisecond, time.Millisecond),
		enginehost.WithSuperviseCooldown(time.Hour))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}

	// A slow-crasher: every restart's startProcess SUCCEEDS (passes /health) and the
	// instance then dies shortly after. Model that by exiting the current (newest)
	// process right before each pass, so the pass observes it down and restarts it —
	// a restart that itself succeeds yet dies before the next pass. Advance the clock
	// past the (tiny) backoff each pass so backoff never masks an attempt.
	now := time.Now()
	for i := 0; i < 8; i++ {
		starter.proc(starter.callCount() - 1).exit()
		enginehost.SuperviseOnce(sup, context.Background(), now)
		now = now.Add(time.Second)
	}

	// Bounded to the cap: initial spawn + exactly 3 restarts, even though 8 passes
	// each saw the instance down. Without the window bound, the brief health-pass on
	// each restart would reset the counter and it would be restarted every pass.
	if got := starter.callCount(); got != 1+3 {
		t.Errorf("successful starts = %d, want 4 (initial + 3 capped restarts); slow-crasher not bounded", got)
	}
	// And it STAYS degraded through the cooldown — the trailing passes attempt nothing.
	if !rr.isDegraded(10) {
		t.Error("slow-crasher source not degraded after hitting the restart cap")
	}
	if degraded, _, ok := enginehost.InstanceSupervisionState(l, "k1"); !ok || !degraded {
		t.Errorf("state ok=%v degraded=%v, want managed + degraded during cooldown", ok, degraded)
	}
}

// TestSupervise_PassPanicRecovers proves a panic inside a supervision pass is
// recovered so the background loop survives and keeps running: the first pass
// panics, and later passes still execute (the loop is not taken down with the
// process).
func TestSupervise_PassPanicRecovers(t *testing.T) {
	starter := &fakeStarter{} // closeOnSignal false: the instance stays alive
	rr := newFakeRerouter()

	var armed atomic.Bool
	var supCalls atomic.Int32
	// Healthy during setup; once armed, panic on the FIRST supervise probe then
	// report healthy — so the panic lands inside a pass, not during the spawn.
	prober := func(string) error {
		if !armed.Load() {
			return nil
		}
		if supCalls.Add(1) == 1 {
			panic("supervise pass boom")
		}
		return nil
	}
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, prober,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(2*time.Millisecond))

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	armed.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup.Start(ctx)

	// The first pass panics and is recovered; the loop must run further passes.
	deadline := time.After(2 * time.Second)
	for supCalls.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("supervise loop did not survive a panicking pass (supCalls=%d)", supCalls.Load())
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
}

// TestSupervise_StartLoopDisabledIdles proves a 0 interval disables supervision:
// the loop idles (re-reading the setting) and never runs a pass, so a dead
// instance is left untouched — the hot-reloadable off switch.
func TestSupervise_StartLoopDisabledIdles(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rr := newFakeRerouter()
	l, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithRerouter(rr))
	sup := enginehost.NewSupervisor(l, fixedInterval(0)) // disabled

	if _, err := l.EnsureProfile(context.Background(), profileWithSources("k1", 10)); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	starter.proc(0).exit()

	ctx, cancel := context.WithCancel(context.Background())
	sup.Start(ctx)
	time.Sleep(30 * time.Millisecond) // give a disabled loop time to (not) act
	cancel()

	if starter.callCount() != 1 {
		t.Errorf("disabled supervisor restarted the instance (%d spawns), want 1 (idle)", starter.callCount())
	}
	if rr.isDegraded(10) {
		t.Error("disabled supervisor degraded a source, want no supervision action")
	}
}
