package enginehost

import (
	"context"
	"time"
)

// export_test.go exposes unexported helpers to the black-box enginehost_test
// package so pure/internal logic can be pinned directly without spawning a real
// process. It is compiled only under `go test`.

// DataDirFor exposes dataDirFor.
func DataDirFor(base, key string) string { return dataDirFor(base, key) }

// FsSafeKey exposes fsSafeKey.
func FsSafeKey(key string) string { return fsSafeKey(key) }

// BuildHostEnv exposes buildHostEnv so the per-instance env shape is testable
// without exec.
func BuildHostEnv(base []string, port int, dataDir string, disableKCEF bool) []string {
	return buildHostEnv(base, port, dataDir, disableKCEF)
}

// SeedKCEF exposes the (best-effort) KCEF-seeding step so it can be driven
// against a temp dir + fake bundle without a spawn.
func SeedKCEF(l *Launcher, dataDir string) { l.seedKCEF(dataDir) }

// LinkSharedExtensions exposes the (fail-loud) shared-extensions symlink step so
// it can be driven against temp dirs without a spawn.
func LinkSharedExtensions(l *Launcher, profileDataDir string) error {
	return l.linkSharedExtensions(profileDataDir)
}

// HTTPHealthProber exposes the production HTTP prober constructor.
func HTTPHealthProber(timeout time.Duration) HealthProber { return newHTTPHealthProber(timeout) }

// HTTPStatusProber exposes the bounded production status prober constructor.
func HTTPStatusProber(timeout time.Duration) StatusProber { return newHTTPStatusProber(timeout) }

// ExhaustionFingerprint exposes the canonical physical-running identity used by
// managed-profile recovery.
func ExhaustionFingerprint(status EngineStatus) (string, bool) { return status.exhaustionFingerprint() }

// AllocFreePort exposes the production free-port allocator.
func AllocFreePort() (int, error) { return allocFreePort() }

// SuperviseOnce drives ONE supervision pass at the given wall-clock time, so a
// test can exercise the probe→degrade→restart→restore state machine
// deterministically without the background goroutine or real timers.
func SuperviseOnce(s *Supervisor, ctx context.Context, now time.Time) { s.superviseOnce(ctx, now) }

// InstanceSupervisionState reports a managed instance's supervision state
// (whether its sources are degraded + its consecutive-restart-failure count) so a
// test can assert the state machine's transitions. ok is false when no instance
// for key is managed.
func InstanceSupervisionState(l *Launcher, key string) (degraded bool, failures int, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	mi, present := l.instances[key]
	if !present {
		return false, 0, false
	}
	return mi.degraded, mi.restartFailures, true
}

// ExhaustionEvidenceState is the bounded managed-profile proof state exposed to
// black-box tests.
type ExhaustionEvidenceState struct {
	Fingerprint        string
	CompletionSequence int64
	Consecutive        int
	FirstSampleAt      time.Time
	NextSampleAt       time.Time
	NextEligibleAt     time.Time
}

// InstanceExhaustionEvidence reports one managed instance's bounded exhaustion
// proof so cancellation and health-mode transitions can be asserted directly.
func InstanceExhaustionEvidence(l *Launcher, key string) (ExhaustionEvidenceState, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	mi, present := l.instances[key]
	if !present {
		return ExhaustionEvidenceState{}, false
	}
	return ExhaustionEvidenceState{
		Fingerprint:        mi.exhaustionFingerprint,
		CompletionSequence: mi.exhaustionCompletionSequence,
		Consecutive:        mi.exhaustionConsecutive,
		FirstSampleAt:      mi.exhaustionFirstSampleAt,
		NextSampleAt:       mi.exhaustionNextSampleAt,
		NextEligibleAt:     mi.exhaustionNextEligibleAt,
	}, true
}
