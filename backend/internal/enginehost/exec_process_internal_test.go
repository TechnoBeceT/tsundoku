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

func (s *fakeProcessGroupSystem) SignalGroup(_ int, signal syscall.Signal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signals = append(s.signals, signal)
	if signal == 0 {
		return s.probeErr
	}
	return nil
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

func TestExecProcessRecognizesOriginalDescendantsAfterLeaderReap(t *testing.T) {
	system := &fakeProcessGroupSystem{current: identityRead{err: os.ErrNotExist}}
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
	proc := &execProcess{pgid: 42, leaderStartTime: 100, groups: system}
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

func TestExecProcessRefusesGroupRecycledBetweenProbeAndSignal(t *testing.T) {
	system := &fakeProcessGroupSystem{current: identityRead{startTime: 100}}
	proc := &execProcess{pgid: 42, leaderStartTime: 100, groups: system}
	if exists, err := proc.GroupExists(); err != nil || !exists {
		t.Fatalf("initial GroupExists = %v, %v; want original group", exists, err)
	}
	system.current = identityRead{startTime: 200}

	if err := proc.Signal(syscall.SIGKILL); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Signal after recycle = %v, want ESRCH", err)
	}
	if got := system.nonProbeSignals(); len(got) != 0 {
		t.Fatalf("signalled recycled group: %v", got)
	}
}

func TestExecProcessRefusesRecycleDuringDescendantSignalChecks(t *testing.T) {
	system := &fakeProcessGroupSystem{
		reads: []identityRead{
			{err: os.ErrNotExist},
			{startTime: 200},
		},
		current: identityRead{startTime: 200},
	}
	proc := &execProcess{pgid: 42, leaderStartTime: 100, groups: system}

	if err := proc.Signal(syscall.SIGKILL); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Signal during recycle = %v, want ESRCH", err)
	}
	if got := system.nonProbeSignals(); len(got) != 0 {
		t.Fatalf("signalled group after descendant probe recycled: %v", got)
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
