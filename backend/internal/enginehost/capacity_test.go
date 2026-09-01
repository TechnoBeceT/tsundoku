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
