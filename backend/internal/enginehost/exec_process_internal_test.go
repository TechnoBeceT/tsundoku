package enginehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type identityRead struct {
	startTime uint64
	err       error
}

type fakeProcessGroupSystem struct {
	mu               sync.Mutex
	reads            []identityRead
	current          identityRead
	leaderExited     bool
	exitLeaderOnTERM bool
	disappearOnKILL  bool
	probeErr         error
	signals          []syscall.Signal
}

func (s *fakeProcessGroupSystem) LeaderStartTime(int) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.reads) > 0 {
		read := s.reads[0]
		s.reads = s.reads[1:]
		return read.startTime, read.err
	}
	return s.current.startTime, s.current.err
}

func (s *fakeProcessGroupSystem) LeaderExited(int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leaderExited, nil
}

func (s *fakeProcessGroupSystem) SignalGroup(_ int, signal syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if signal == 0 {
		s.signals = append(s.signals, signal)
		return s.probeErr
	}
	if s.current.err != nil || s.current.startTime != 100 {
		return syscall.ESRCH
	}
	s.signals = append(s.signals, signal)
	if signal == syscall.SIGTERM && s.exitLeaderOnTERM {
		s.leaderExited = true
	}
	if signal == syscall.SIGKILL && s.disappearOnKILL {
		s.current = identityRead{err: os.ErrNotExist}
		s.probeErr = syscall.ESRCH
	}
	return nil
}

type blockingSignalGroupSystem struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (s *blockingSignalGroupSystem) LeaderStartTime(int) (uint64, error) { return 100, nil }
func (s *blockingSignalGroupSystem) LeaderExited(int) (bool, error)      { return false, nil }
func (s *blockingSignalGroupSystem) SignalGroup(int, syscall.Signal) error {
	close(s.entered)
	<-s.release
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return nil
}

func TestExecProcessSpontaneousExitAutonomouslyKillsGroupAndReapsLeader(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start helper: %v", err)
	}
	system := &fakeProcessGroupSystem{
		current:      identityRead{startTime: 100},
		leaderExited: true,
	}
	proc := &execProcess{
		cmd: cmd, pgid: 42, leaderStartTime: 100, groups: system,
		done: make(chan struct{}), reaped: make(chan struct{}),
		terminalSignal: make(chan struct{}),
	}
	go proc.reapAfterGroupQuiesces()

	select {
	case <-proc.Done():
	case <-time.After(time.Second):
		t.Fatal("leader exit was not observed")
	}
	select {
	case <-proc.Reaped():
	case <-time.After(time.Second):
		// Unblock the old implementation so the failed regression does not leave
		// its helper as a zombie until the test binary exits.
		cleanupErr := proc.Kill()
		<-proc.Reaped()
		t.Fatalf("exited leader needed external cleanup before exact Wait: %v", cleanupErr)
	}
	if got := system.nonProbeSignals(); len(got) != 1 || got[0] != syscall.SIGKILL {
		t.Fatalf("reaper group signals = %v, want one terminal KILL", got)
	}
}

func TestTerminateProcessGroupPreservesConfiguredGraceAfterLeaderExit(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start helper: %v", err)
	}
	graceWaitStarted := make(chan time.Duration, 1)
	graceExpired := make(chan time.Time)
	graceStartedAt := time.Unix(1_700_000_000, 0)
	graceTimes := make(chan time.Time, 2)
	graceTimes <- graceStartedAt
	graceTimes <- graceStartedAt.Add(2 * time.Second)
	system := &fakeProcessGroupSystem{
		current:          identityRead{startTime: 100},
		exitLeaderOnTERM: true,
		disappearOnKILL:  true,
	}
	proc := &execProcess{
		cmd: cmd, pgid: 42, leaderStartTime: 100, groups: system,
		done: make(chan struct{}), reaped: make(chan struct{}),
		terminalSignal: make(chan struct{}),
		graceNow:       func() time.Time { return <-graceTimes },
		graceAfter: func(grace time.Duration) <-chan time.Time {
			graceWaitStarted <- grace
			return graceExpired
		},
	}
	go proc.reapAfterGroupQuiesces()

	const configuredGrace = 5 * time.Second
	terminated := make(chan bool, 1)
	go func() {
		terminated <- terminateProcessGroup(context.Background(), proc, configuredGrace)
	}()
	assertConfiguredGraceWindow(t, proc, system, configuredGrace, graceWaitStarted, graceExpired, terminated)
}

func assertConfiguredGraceWindow(t *testing.T, proc *execProcess, system *fakeProcessGroupSystem, configuredGrace time.Duration, graceWaitStarted <-chan time.Duration, graceExpired chan time.Time, terminated <-chan bool) {
	t.Helper()
	requireGraceWaitStarted(t, proc, configuredGrace, graceWaitStarted)
	assertProcessGroupSignals(t, system, []syscall.Signal{syscall.SIGTERM}, "signals before grace expiry")
	assertProcessNotReaped(t, proc)
	close(graceExpired)
	requireProcessReaped(t, proc, time.Second, "bounded autonomous fallback did not reap after grace expiry")
	requireTerminatedGroupAbsent(t, terminated)
	assertProcessGroupSignals(t, system, []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}, "complete graceful-stop signals")
}

func requireGraceWaitStarted(t *testing.T, proc *execProcess, configuredGrace time.Duration, graceWaitStarted <-chan time.Duration) {
	t.Helper()
	select {
	case got := <-graceWaitStarted:
		wantRemaining := configuredGrace - 2*time.Second
		if got != wantRemaining {
			t.Fatalf("reaper remaining grace = %v, want %v from the TERM deadline", got, wantRemaining)
		}
	case <-time.After(time.Second):
		_ = proc.Kill()
		<-proc.Reaped()
		t.Fatal("reaper did not enter the active graceful-stop window")
	}
}

func assertProcessGroupSignals(t *testing.T, system *fakeProcessGroupSystem, want []syscall.Signal, message string) {
	t.Helper()
	if got := system.nonProbeSignals(); !slices.Equal(got, want) {
		t.Fatalf("%s = %v, want %v", message, got, want)
	}
}

func assertProcessNotReaped(t *testing.T, proc *execProcess) {
	t.Helper()
	select {
	case <-proc.Reaped():
		t.Fatal("leader was reaped before configured grace expired")
	default:
	}
}

func requireTerminatedGroupAbsent(t *testing.T, terminated <-chan bool) {
	t.Helper()
	select {
	case gone := <-terminated:
		if !gone {
			t.Fatal("graceful termination did not observe complete group absence")
		}
	case <-time.After(time.Second):
		t.Fatal("graceful termination did not finish after autonomous fallback")
	}
}

func TestExecProcessPinsPGIDThroughFinalSignalSyscall(t *testing.T) {
	system := &blockingSignalGroupSystem{entered: make(chan struct{}), release: make(chan struct{})}
	proc := &execProcess{
		pgid: 42, leaderStartTime: 100, groups: system,
		terminalSignal: make(chan struct{}),
	}
	signalDone := make(chan error, 1)
	go func() { signalDone <- proc.Signal(syscall.SIGKILL) }()
	<-system.entered

	waitCalled := make(chan struct{})
	reapDone := make(chan struct{})
	go func() {
		proc.finishReap(func() { close(waitCalled) })
		close(reapDone)
	}()
	select {
	case <-waitCalled:
		t.Fatal("Wait released the PGID while the final group syscall was in flight")
	case <-time.After(20 * time.Millisecond):
	}
	close(system.release)
	if err := <-signalDone; err != nil {
		t.Fatalf("Signal: %v", err)
	}
	<-reapDone
	if err := proc.Signal(syscall.SIGTERM); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Signal after Wait = %v, want ESRCH without numeric group syscall", err)
	}
	system.mu.Lock()
	calls := system.calls
	system.mu.Unlock()
	if calls != 1 {
		t.Fatalf("numeric group syscalls = %d, want only the syscall completed before Wait", calls)
	}
}

func (s *fakeProcessGroupSystem) nonProbeSignals() []syscall.Signal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var signals []syscall.Signal
	for _, signal := range s.signals {
		if signal != 0 {
			signals = append(signals, signal)
		}
	}
	return signals
}

func TestExecProcessRecognizesAndSignalsOriginalLeader(t *testing.T) {
	system := &fakeProcessGroupSystem{current: identityRead{startTime: 100}}
	assertOwnedGroupCanBeSignalled(t, system)
}

func TestExecProcessSignalsOriginalDescendantsWhileExitedLeaderPinsGroup(t *testing.T) {
	system := &fakeProcessGroupSystem{current: identityRead{startTime: 100}}
	assertOwnedGroupCanBeSignalled(t, system)
}

func TestExecProcessRecognizesCompleteGroupDisappearance(t *testing.T) {
	system := &fakeProcessGroupSystem{
		current: identityRead{err: os.ErrNotExist}, probeErr: syscall.ESRCH,
	}
	assertUnknownGroupIsNeverSignalled(t, system)
}

func TestExecProcessMissingLeaderRetainsLiveDescendantGroup(t *testing.T) {
	system := &fakeProcessGroupSystem{
		current: identityRead{err: os.ErrNotExist}, probeErr: nil,
	}
	proc := &execProcess{pgid: 42, leaderStartTime: 100, groups: system, waited: true}
	exists, err := proc.GroupExists()
	if err != nil || !exists {
		t.Fatalf("GroupExists = %v, %v; want true, nil while descendant group exists", exists, err)
	}
	if got := system.nonProbeSignals(); len(got) != 0 {
		t.Fatalf("non-probe signals after Wait = %v, want none", got)
	}
}

func TestExecProcessRecognizesRecycledGroupLeader(t *testing.T) {
	system := &fakeProcessGroupSystem{current: identityRead{startTime: 200}}
	assertUnknownGroupIsNeverSignalled(t, system)
}

func assertOwnedGroupCanBeSignalled(t *testing.T, system *fakeProcessGroupSystem) {
	t.Helper()
	proc := &execProcess{pgid: 42, leaderStartTime: 100, groups: system}
	exists, err := proc.GroupExists()
	if err != nil || !exists {
		t.Fatalf("GroupExists = %v, %v; want true, nil", exists, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if got := system.nonProbeSignals(); len(got) != 1 || got[0] != syscall.SIGTERM {
		t.Fatalf("non-probe signals = %v, want TERM", got)
	}
}

func assertUnknownGroupIsNeverSignalled(t *testing.T, system *fakeProcessGroupSystem) {
	t.Helper()
	proc := &execProcess{pgid: 42, leaderStartTime: 100, groups: system, waited: true}
	exists, err := proc.GroupExists()
	if err != nil || exists {
		t.Fatalf("GroupExists = %v, %v; want false, nil", exists, err)
	}
	if err := proc.Signal(syscall.SIGTERM); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Signal recycled/absent group error = %v, want ESRCH", err)
	}
	if got := system.nonProbeSignals(); len(got) != 0 {
		t.Fatalf("signalled unknown group: %v", got)
	}
}

func TestParseProcStartTimeHandlesComplexComm(t *testing.T) {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[19] = "987654321"
	stat := fmt.Sprintf("42 (engine ) host helper) %s\n", strings.Join(fields, " "))

	got, err := parseProcStartTime([]byte(stat))
	if err != nil {
		t.Fatalf("parseProcStartTime: %v", err)
	}
	if got != 987654321 {
		t.Fatalf("starttime = %d, want 987654321", got)
	}
}

func TestExecStarterCreatesAndSignalsDedicatedProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "host")
	//nolint:gosec // This owner-only test helper must be directly executable.
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	proc, err := (execStarter{hostBin: script}).Start(41001, dir, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { cleanupRunningProcess(proc) })

	pgid, err := syscall.Getpgid(proc.Pid())
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid != proc.GroupID() || pgid != proc.Pid() {
		t.Fatalf("pid/pgid/reported = %d/%d/%d, want one dedicated group", proc.Pid(), pgid, proc.GroupID())
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal group: %v", err)
	}
	requireProcessDone(t, proc, 2*time.Second, "group TERM did not terminate the JVM")
	execProc := proc.(*execProcess)
	requireProcessReaped(t, execProc, 2*time.Second, "reaper did not autonomously finalize the terminated group")
	requireProcessGroupAbsent(t, proc, "process group still exists/error")
	if err := proc.Signal(syscall.SIGTERM); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal absent group error = %v, want ESRCH", err)
	}
}

func TestExecStarterKillsDescendantGroupAndReapsExitedLeader(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "host-with-descendant")
	contents := "#!/bin/sh\nsh -c 'trap \"\" HUP TERM; while :; do sleep 1; done' &\necho $! > \"$TSUNDOKU_ENGINE_DATA/child.pid\"\nsleep 0.05\nexit 0\n"
	//nolint:gosec // This owner-only test helper must be directly executable.
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	proc, err := (execStarter{hostBin: script}).Start(41001, dir, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Kill() })

	requireProcessDone(t, proc, 2*time.Second, "leader exit was not observed")
	execProc := proc.(*execProcess)
	requireProcessReaped(t, execProc, 2*time.Second, "reaper did not kill the descendant group and reap the exited leader")
	requireProcessGroupAbsent(t, proc, "GroupExists after reap")
}

func cleanupRunningProcess(proc RunningProcess) {
	_ = proc.Kill()
	select {
	case <-proc.Done():
	case <-time.After(time.Second):
	}
}

func requireProcessDone(t *testing.T, proc RunningProcess, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-proc.Done():
	case <-time.After(timeout):
		t.Fatal(message)
	}
}

func requireProcessReaped(t *testing.T, proc *execProcess, timeout time.Duration, message string) {
	t.Helper()
	select {
	case <-proc.Reaped():
	case <-time.After(timeout):
		t.Fatal(message)
	}
}

func requireProcessGroupAbsent(t *testing.T, proc RunningProcess, message string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		exists, probeErr := proc.GroupExists()
		if probeErr == nil && !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %v/%v, want false/nil", message, exists, probeErr)
		}
		time.Sleep(time.Millisecond)
	}
}
