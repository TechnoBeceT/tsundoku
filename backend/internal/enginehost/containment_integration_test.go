package enginehost_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

// TestManagedProfileContainmentIntegration drives the complete managed-profile
// proof: physical-work progress invalidates partial evidence, six subsequent
// stable samples restart the same-port process once, and the ten-minute cooldown
// suppresses a second stable episode.
func TestManagedProfileContainmentIntegration(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	var (
		mu      sync.Mutex
		current = exhaustedStatus(70, 0, exhaustedSources...)
	)
	statusProber := func(context.Context, string) (enginehost.EngineStatus, error) {
		mu.Lock()
		defer mu.Unlock()
		status := current
		status.BusiestSources = append([]enginehost.EngineSourceStatus(nil), current.BusiestSources...)
		return status, nil
	}
	var diagnostics []enginehost.ExhaustionDiagnostic
	l, _ := newTestLauncher(
		t,
		enginehost.EngineHostLauncherConfig{},
		starter,
		okProber,
		enginehost.WithStatusProber(statusProber),
		enginehost.WithExhaustionDiagnosticSink(func(_ context.Context, diagnostic enginehost.ExhaustionDiagnostic) {
			diagnostics = append(diagnostics, diagnostic)
		}),
	)
	supervisor := enginehost.NewSupervisor(l, fixedInterval(30*time.Second))
	instance, err := l.EnsureProfile(context.Background(), profileWithSources("contained-profile", 11, 22, 33, 44))
	requireManagedInstance(t, instance.BaseURL, err)

	started := time.Unix(1_700_000_000, 0)
	for i := 0; i < 3; i++ {
		enginehost.SuperviseOnce(supervisor, context.Background(), started.Add(time.Duration(i)*30*time.Second))
	}

	// Completion progress changes independently from the running-work fingerprint
	// and starts a fresh evidence episode.
	mu.Lock()
	current.CompletionSequence = 71
	mu.Unlock()
	enginehost.SuperviseOnce(supervisor, context.Background(), started.Add(3*30*time.Second))
	requireFreshEvidence(t, l)

	for i := 4; i <= 8; i++ {
		enginehost.SuperviseOnce(supervisor, context.Background(), started.Add(time.Duration(i)*30*time.Second))
	}
	requireSingleContainedRestart(t, starter, diagnostics)

	// The relaunched fake reports the same stable exhaustion immediately. A
	// complete second proof still cannot restart during the ten-minute cooldown.
	for i := 9; i <= 14; i++ {
		enginehost.SuperviseOnce(supervisor, context.Background(), started.Add(time.Duration(i)*30*time.Second))
	}
	requireCooldownSuppression(t, starter, diagnostics)
}

func requireManagedInstance(t *testing.T, baseURL string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	if baseURL != "http://127.0.0.1:41001" {
		t.Fatalf("managed base URL = %q, want stable same-port route", baseURL)
	}
}

func requireFreshEvidence(t *testing.T, launcher *enginehost.Launcher) {
	t.Helper()
	evidence, ok := enginehost.InstanceExhaustionEvidence(launcher, "contained-profile")
	if !ok || evidence.Consecutive != 1 {
		t.Fatalf("evidence after progress = %+v, ok=%v; want fresh sample", evidence, ok)
	}
}

func requireSingleContainedRestart(t *testing.T, starter *fakeStarter, diagnostics []enginehost.ExhaustionDiagnostic) {
	t.Helper()
	if got := starter.callCount(); got != 2 {
		t.Fatalf("process starts after stable proof = %d, want initial plus one restart", got)
	}
	if !starter.proc(0).wasSignalled() {
		t.Fatal("exhausted process was not stopped through the managed lifecycle")
	}
	if got := starter.lastCall().port; got != 41001 {
		t.Fatalf("restart port = %d, want original 41001", got)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %d, want one pre-restart bundle", len(diagnostics))
	}
	diagnostic := diagnostics[0]
	if diagnostic.ProfileKey != "contained-profile" || diagnostic.PID != 1 || diagnostic.Fingerprint != "8|8|11:2,22:2,33:2,44:2" {
		t.Fatalf("diagnostic = %+v, want bounded process/profile/occupancy evidence", diagnostic)
	}
}

func requireCooldownSuppression(t *testing.T, starter *fakeStarter, diagnostics []enginehost.ExhaustionDiagnostic) {
	t.Helper()
	if got := starter.callCount(); got != 2 {
		t.Fatalf("process starts during recovery cooldown = %d, want 2", got)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics during cooldown = %d, want no second restart bundle", len(diagnostics))
	}
}
