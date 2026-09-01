package enginehost_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/technobecet/tsundoku/internal/enginehost"
	"github.com/technobecet/tsundoku/internal/engineroute"
)

func TestPrepareProfiles_AdmitsCanonicalKCEFKeysWithinDefaultReservation(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber)
	a := profile("a")
	z := profile("z")

	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{z, a})

	if _, err := launcher.EnsureProfile(context.Background(), z); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(z) error = %v, want ErrKCEFCapacity", err)
	}
	if _, err := launcher.EnsureProfile(context.Background(), a); err != nil {
		t.Fatalf("EnsureProfile(a): %v", err)
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts = %d, want only canonical key a", got)
	}
}

func TestPrepareProfiles_DefaultOffAdmitsOnlyTwoKCEFGroups(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	profiles := []engineroute.Profile{profile("c"), profile("b"), profile("a")}
	launcher.PrepareProfiles(context.Background(), profiles)

	for _, p := range []engineroute.Profile{profile("a"), profile("b")} {
		if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
			t.Fatalf("EnsureProfile(%s): %v", p.Key, err)
		}
	}
	if _, err := launcher.EnsureProfile(context.Background(), profile("c")); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(c) error = %v, want ErrKCEFCapacity", err)
	}
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts = %d, want 2", got)
	}
}

func TestPrepareProfiles_RetainsReadyDesiredProfileWithoutThrash(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber)
	z := profile("z-ready")
	a := profile("a-new")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{z})
	if _, err := launcher.EnsureProfile(context.Background(), z); err != nil {
		t.Fatalf("EnsureProfile(z): %v", err)
	}

	for range 2 {
		launcher.PrepareProfiles(context.Background(), []engineroute.Profile{a, z})
		if _, err := launcher.EnsureProfile(context.Background(), a); !errors.Is(err, enginehost.ErrKCEFCapacity) {
			t.Fatalf("EnsureProfile(a) error = %v, want capacity degradation", err)
		}
		if _, err := launcher.EnsureProfile(context.Background(), z); err != nil {
			t.Fatalf("EnsureProfile(z reuse): %v", err)
		}
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts = %d, want retained profile reused without replacement", got)
	}
	if starter.proc(0).wasSignalled() {
		t.Fatal("retained ready profile was stopped")
	}
}

func TestPrepareProfiles_DeadDesiredProfileDoesNotDisplaceCanonicalCandidate(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	z := profile("z-dead")
	a := profile("a-new")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{z})
	if _, err := launcher.EnsureProfile(context.Background(), z); err != nil {
		t.Fatalf("EnsureProfile(z): %v", err)
	}
	starter.proc(0).exit()

	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{z, a})
	if _, err := launcher.EnsureProfile(context.Background(), z); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(z dead) error = %v, want canonical capacity degradation", err)
	}
	if _, err := launcher.EnsureProfile(context.Background(), a); err != nil {
		t.Fatalf("EnsureProfile(a): %v", err)
	}
}

func TestPrepareProfiles_FailedLiveDesiredProfileDoesNotDisplaceCanonicalCandidate(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	failed := readyKCEFStatus()
	failed.KCEF = enginehost.KCEFStatus{
		State:     enginehost.KCEFStateFailed,
		ErrorCode: kcefError(enginehost.KCEFErrorInitFailed),
	}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithStatusProber(sequenceStatus(readyKCEFStatus(), failed, readyKCEFStatus())),
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	z := profile("z-failed")
	a := profile("a-new")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{z})
	if _, err := launcher.EnsureProfile(context.Background(), z); err != nil {
		t.Fatalf("EnsureProfile(z): %v", err)
	}

	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{z, a})
	if _, err := launcher.EnsureProfile(context.Background(), z); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(z failed) error = %v, want canonical capacity degradation", err)
	}
	if _, err := launcher.EnsureProfile(context.Background(), a); err != nil {
		t.Fatalf("EnsureProfile(a): %v", err)
	}
	if !starter.proc(0).wasSignalled() {
		t.Fatal("failed live generation was not retired before canonical replacement")
	}
}

func TestPrepareProfiles_RetirementDegradeSurvivesReplacementHealthUntilPublication(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rerouter := newFakeRerouter()
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithRerouter(rerouter),
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	oldProfile := engineroute.Profile{Key: "old-key", SourceIDs: []int64{10}, KCEFEnabled: true}
	initial := launcher.PrepareProfiles(context.Background(), []engineroute.Profile{oldProfile})
	if _, err := launcher.EnsureProfile(context.Background(), oldProfile); err != nil {
		t.Fatalf("EnsureProfile(old): %v", err)
	}
	initial.CompletePublication()

	newProfile := engineroute.Profile{Key: "new-key", SourceIDs: []int64{10}, KCEFEnabled: true}
	publication := launcher.PrepareProfiles(context.Background(), []engineroute.Profile{newProfile})
	if _, err := launcher.EnsureProfile(context.Background(), newProfile); err != nil {
		t.Fatalf("EnsureProfile(new): %v", err)
	}
	if !rerouter.isDegraded(10) {
		t.Fatal("replacement health reopened source before new base routes were published")
	}

	successor := launcher.PrepareProfiles(context.Background(), []engineroute.Profile{newProfile})
	publication.CompletePublication()
	if !rerouter.isDegraded(10) {
		t.Fatal("stale preparation completion released a newer publication lease")
	}
	successor.CompletePublication()
	if rerouter.isDegraded(10) {
		t.Fatal("publication completion did not release retirement degradation")
	}
}

func TestPrepareProfiles_SameKeyReplacementDegradeSurvivesHealthUntilPublication(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	rerouter := newFakeRerouter()
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithRerouter(rerouter),
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	p := engineroute.Profile{Key: "same-key", SourceIDs: []int64{10}, KCEFEnabled: true}
	initial := launcher.PrepareProfiles(context.Background(), []engineroute.Profile{p})
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile(initial): %v", err)
	}
	initial.CompletePublication()
	starter.proc(0).exitJVM()

	publication := launcher.PrepareProfiles(context.Background(), []engineroute.Profile{p})
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile(replacement): %v", err)
	}
	if !rerouter.isDegraded(10) {
		t.Fatal("same-key replacement health reopened the stale base route before publication")
	}

	publication.CompletePublication()
	if rerouter.isDegraded(10) {
		t.Fatal("publication completion did not release same-key replacement degradation")
	}
}

func TestPrepareProfiles_ReapsObsoleteGroupBeforeReplacementStart(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	oldProfile := profile("old")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{oldProfile})
	if _, err := launcher.EnsureProfile(context.Background(), oldProfile); err != nil {
		t.Fatalf("EnsureProfile(old): %v", err)
	}

	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("new")})
	if exists, err := starter.proc(0).GroupExists(); err != nil || exists {
		t.Fatalf("obsolete group exists/error = %v/%v, want fully absent before replacement", exists, err)
	}
	if _, err := launcher.EnsureProfile(context.Background(), profile("new")); err != nil {
		t.Fatalf("EnsureProfile(new): %v", err)
	}
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts = %d, want old then replacement", got)
	}
}

func TestPrepareProfiles_ExitedJVMDescendantsKeepCapacityUntilGroupAbsent(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber)
	oldProfile := profile("old")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{oldProfile})
	if _, err := launcher.EnsureProfile(context.Background(), oldProfile); err != nil {
		t.Fatalf("EnsureProfile(old): %v", err)
	}
	old := starter.proc(0)
	old.keepGroupOnKill = true
	old.exitJVM()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	launcher.PrepareProfiles(ctx, []engineroute.Profile{profile("new")})
	if _, err := launcher.EnsureProfile(context.Background(), profile("new")); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(new) with live descendants = %v, want ErrKCEFCapacity", err)
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts with live obsolete descendants = %d, want 1", got)
	}

	old.setGroupState(false, nil)
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("new")})
	if _, err := launcher.EnsureProfile(context.Background(), profile("new")); err != nil {
		t.Fatalf("EnsureProfile(new) after confirmed absence: %v", err)
	}
}

func TestPrepareProfiles_LingeringObsoleteGroupBlocksAllReplacementAdmission(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)),
		enginehost.WithStopGrace(2*time.Millisecond))
	oldProfile := profile("old")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{oldProfile})
	if _, err := launcher.EnsureProfile(context.Background(), oldProfile); err != nil {
		t.Fatalf("EnsureProfile(old): %v", err)
	}
	old := starter.proc(0)
	old.keepGroupOnKill = true

	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("new-a"), profile("new-b")})
	if _, err := launcher.EnsureProfile(context.Background(), profile("new-a")); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(new-a) with obsolete group = %v, want ErrKCEFCapacity", err)
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts while any obsolete group remains = %d, want 1", got)
	}

	old.setGroupState(false, nil)
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("new-a"), profile("new-b")})
	if _, err := launcher.EnsureProfile(context.Background(), profile("new-a")); err != nil {
		t.Fatalf("EnsureProfile(new-a) after obsolete reap: %v", err)
	}
}

func TestPrepareProfiles_GroupProbeErrorNeverReleasesCapacity(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber)
	oldProfile := profile("old")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{oldProfile})
	if _, err := launcher.EnsureProfile(context.Background(), oldProfile); err != nil {
		t.Fatalf("EnsureProfile(old): %v", err)
	}
	old := starter.proc(0)
	old.exit()
	old.setGroupState(false, errors.New("permission denied"))

	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("new")})
	if _, err := launcher.EnsureProfile(context.Background(), profile("new")); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(new) after uncertain probe = %v, want ErrKCEFCapacity", err)
	}

	old.setGroupState(false, nil)
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("new")})
	if _, err := launcher.EnsureProfile(context.Background(), profile("new")); err != nil {
		t.Fatalf("EnsureProfile(new) after confirmed absence: %v", err)
	}
}

func TestEnsureProfile_SameKeyRestartReusesOneCapacitySlot(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	p := profile("same")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{p})
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile #1: %v", err)
	}
	starter.proc(0).exitJVM()
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{p})
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile #2: %v", err)
	}
	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts = %d, want exactly one replacement", got)
	}
	if exists, _ := starter.proc(0).GroupExists(); exists {
		t.Fatal("same-key replacement started before old group disappeared")
	}
}

func TestEnsureProfile_SameKeyNeverStartsWhilePriorGroupRemains(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	p := profile("same-lingering")
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile #1: %v", err)
	}
	old := starter.proc(0)
	old.keepGroupOnKill = true
	old.exitJVM()

	if _, err := launcher.EnsureProfile(context.Background(), p); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile #2 error = %v, want lingering-group capacity denial", err)
	}
	if got := starter.callCount(); got != 1 {
		t.Fatalf("starts with prior group present = %d, want 1", got)
	}

	old.setGroupState(false, nil)
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile after confirmed absence: %v", err)
	}
}

func TestSupervisor_SameKeyRestartReapsExitedJVMGroupBeforeStart(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithPortAllocator(fixedPortAllocator(41001)))
	p := profile("same-supervised")
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile: %v", err)
	}
	old := starter.proc(0)
	old.exitJVM()

	supervisor := enginehost.NewSupervisor(launcher, fixedInterval(time.Second))
	enginehost.SuperviseOnce(supervisor, context.Background(), time.Now())

	if got := starter.callCount(); got != 2 {
		t.Fatalf("starts = %d, want old generation plus one replacement", got)
	}
	if !old.wasKilled() {
		t.Fatal("supervisor did not kill the exited JVM's descendant group before restart")
	}
	if exists, _ := old.GroupExists(); exists {
		t.Fatal("supervisor started replacement before old group disappeared")
	}
}

func TestPrepareProfiles_CancellationIsBoundedAndKeepsUncertainCapacity(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber)
	oldProfile := profile("old")
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{oldProfile})
	if _, err := launcher.EnsureProfile(context.Background(), oldProfile); err != nil {
		t.Fatalf("EnsureProfile(old): %v", err)
	}
	starter.proc(0).keepGroupOnKill = true

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	launcher.PrepareProfiles(ctx, []engineroute.Profile{profile("new")})
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("PrepareProfiles cancellation took %s, want prompt return", elapsed)
	}
	if _, err := launcher.EnsureProfile(context.Background(), profile("new")); !errors.Is(err, enginehost.ErrKCEFCapacity) {
		t.Fatalf("EnsureProfile(new) after canceled reap = %v, want ErrKCEFCapacity", err)
	}
}

func TestPrepareProfiles_NonKCEFProfilesDoNotConsumeBrowserCapacity(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: true}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter, okProber,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return disabledKCEFStatus(), nil
		}),
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002, 41003)))
	profiles := []engineroute.Profile{{Key: "off-a"}, {Key: "off-b"}, {Key: "off-c"}}
	launcher.PrepareProfiles(context.Background(), profiles)
	for _, p := range profiles {
		if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
			t.Fatalf("EnsureProfile(%s): %v", p.Key, err)
		}
	}
	if got := starter.callCount(); got != 3 {
		t.Fatalf("non-KCEF starts = %d, want 3", got)
	}
}

func TestRetire_CanceledContextStillKillsNonKCEFGroupPromptly(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return disabledKCEFStatus(), nil
		}))
	p := engineroute.Profile{Key: "off"}
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{p})
	if _, err := launcher.EnsureProfile(context.Background(), p); err != nil {
		t.Fatalf("EnsureProfile(off): %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	launcher.Retire(ctx, map[string]bool{})
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("Retire cancellation took %s, want prompt return", elapsed)
	}
	if !starter.proc(0).wasKilled() {
		t.Fatal("canceled retirement left non-KCEF process group running")
	}
}

func TestPrepareProfiles_ReapsUncertainNonKCEFReadinessFailureWithoutChargingCapacity(t *testing.T) {
	starter := &fakeStarter{
		closeOnSignal: false, keepGroupOnKill: true, groupProbeErr: errors.New("probe uncertain"),
	}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{DefaultKCEFEnabled: true}, starter,
		func(_ context.Context, baseURL string) error {
			if baseURL == "http://127.0.0.1:41001" {
				return errors.New("not ready")
			}
			return nil
		},
		enginehost.WithLaunchReadinessTimeout(2*time.Millisecond),
		enginehost.WithStopGrace(2*time.Millisecond),
		enginehost.WithPortAllocator(fixedPortAllocator(41001, 41002)))
	off := engineroute.Profile{Key: "off-failed"}
	if _, err := launcher.EnsureProfile(context.Background(), off); err == nil {
		t.Fatal("EnsureProfile(off) succeeded, want readiness failure")
	}
	failed := starter.proc(0)
	if exists, _ := failed.GroupExists(); !exists {
		t.Fatal("test setup lost uncertain non-KCEF group")
	}

	// The uncertain non-KCEF group remains owned but must not consume the one
	// managed KCEF slot left by the default reservation.
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("on")})
	if _, err := launcher.EnsureProfile(context.Background(), profile("on")); err != nil {
		t.Fatalf("EnsureProfile(on) with lingering non-KCEF group: %v", err)
	}

	failed.keepGroupOnKill = false
	failed.setGroupState(true, nil)
	launcher.PrepareProfiles(context.Background(), []engineroute.Profile{profile("on")})
	if exists, _ := failed.GroupExists(); exists {
		t.Fatal("later preparation did not reap uncertain non-KCEF readiness group")
	}
}

func TestPrepareProfiles_ReapsUncertainDetachedNonKCEFGroup(t *testing.T) {
	starter := &fakeStarter{closeOnSignal: false}
	launcher, _ := newTestLauncher(t, enginehost.EngineHostLauncherConfig{}, starter, okProber,
		enginehost.WithStatusProber(func(context.Context, string) (enginehost.EngineStatus, error) {
			return disabledKCEFStatus(), nil
		}),
		enginehost.WithStopGrace(2*time.Millisecond))
	off := engineroute.Profile{Key: "off-detached"}
	if _, err := launcher.EnsureProfile(context.Background(), off); err != nil {
		t.Fatalf("EnsureProfile(off): %v", err)
	}
	retired := starter.proc(0)
	retired.keepGroupOnKill = true
	retired.setGroupState(true, errors.New("probe uncertain"))
	launcher.Retire(context.Background(), map[string]bool{})
	if exists, _ := retired.GroupExists(); !exists {
		t.Fatal("test setup lost uncertain detached non-KCEF group")
	}

	retired.keepGroupOnKill = false
	retired.setGroupState(true, nil)
	launcher.PrepareProfiles(context.Background(), nil)
	if exists, _ := retired.GroupExists(); exists {
		t.Fatal("later preparation did not reap uncertain detached non-KCEF group")
	}
}
