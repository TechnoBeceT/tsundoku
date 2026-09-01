package enginehost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	mu       sync.Mutex
	reads    []identityRead
	current  identityRead
	probeErr error
	signals  []syscall.Signal
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

func (s *fakeProcessGroupSystem) LeaderExited(int) (bool, error) { return false, nil }

func (s *fakeProcessGroupSystem) OtherGroupMembers(int, int) (bool, error) { return false, nil }

func (s *fakeProcessGroupSystem) SignalGroup(_ int, signal syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.err != nil || s.current.startTime != 100 {
		return syscall.ESRCH
	}
	s.signals = append(s.signals, signal)
	if signal == 0 {
		return s.probeErr
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
func (s *blockingSignalGroupSystem) OtherGroupMembers(int, int) (bool, error) {
	return false, nil
}
func (s *blockingSignalGroupSystem) SignalGroup(int, syscall.Signal) error {
	close(s.entered)
	<-s.release
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return nil
}

func TestExecProcessPinsPGIDThroughFinalSignalSyscall(t *testing.T) {
	system := &blockingSignalGroupSystem{entered: make(chan struct{}), release: make(chan struct{})}
	proc := &execProcess{pgid: 42, leaderStartTime: 100, groups: system}
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
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n"), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	proc, err := (execStarter{hostBin: script}).Start(41001, dir, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		_ = proc.Kill()
		select {
		case <-proc.Done():
		case <-time.After(time.Second):
		}
	})

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
	select {
	case <-proc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("group TERM did not terminate and reap the JVM")
	}
	deadline := time.Now().Add(time.Second)
	for {
		exists, probeErr := proc.GroupExists()
		if probeErr == nil && !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group still exists/error = %v/%v", exists, probeErr)
		}
		time.Sleep(time.Millisecond)
	}
	if err := proc.Signal(syscall.SIGTERM); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("signal absent group error = %v, want ESRCH", err)
	}
}

func TestExecStarterPinsExitedLeaderUntilDescendantGroupIsKilledAndReaped(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "host-with-descendant")
	contents := "#!/bin/sh\nsh -c 'trap \"\" HUP TERM; while :; do sleep 1; done' &\necho $! > \"$TSUNDOKU_ENGINE_DATA/child.pid\"\nsleep 0.05\nexit 0\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	proc, err := (execStarter{hostBin: script}).Start(41001, dir, false)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = proc.Kill() })

	select {
	case <-proc.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("leader exit was not observed")
	}
	execProc := proc.(*execProcess)
	select {
	case <-execProc.Reaped():
		t.Fatal("leader was reaped while its descendant still pinned the group")
	case <-time.After(20 * time.Millisecond):
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("Kill descendant group through pinned exited leader: %v", err)
	}
	select {
	case <-execProc.Reaped():
	case <-time.After(2 * time.Second):
		t.Fatal("leader and killed descendant group were not exactly reaped")
	}
	if exists, probeErr := proc.GroupExists(); probeErr != nil || exists {
		t.Fatalf("GroupExists after reap = %v, %v; want false, nil", exists, probeErr)
	}
}
