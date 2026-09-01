package enginehost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type processGroupSystem interface {
	LeaderStartTime(pid int) (uint64, error)
	LeaderExited(pid int) (bool, error)
	SignalGroup(pgid int, signal syscall.Signal) error
}

type linuxProcessGroupSystem struct{}

func (linuxProcessGroupSystem) LeaderStartTime(pid int) (uint64, error) {
	stat, err := readProcStat(pid)
	if err != nil {
		return 0, err
	}
	return stat.startTime, nil
}

func (linuxProcessGroupSystem) LeaderExited(pid int) (bool, error) {
	stat, err := readProcStat(pid)
	if err != nil {
		return false, err
	}
	return stat.state == 'Z' || stat.state == 'X' || stat.state == 'x', nil
}

func (linuxProcessGroupSystem) SignalGroup(pgid int, signal syscall.Signal) error {
	return syscall.Kill(-pgid, signal)
}

type procStat struct {
	state     byte
	startTime uint64
}

func readProcStat(pid int) (procStat, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) //nolint:gosec // pid is an owned child or numeric /proc entry
	if err != nil {
		return procStat{}, err
	}
	return parseProcStat(stat)
}

func parseProcStartTime(stat []byte) (uint64, error) {
	parsed, err := parseProcStat(stat)
	return parsed.startTime, err
}

func parseProcStat(stat []byte) (procStat, error) {
	// proc_pid_stat(5) makes comm parenthesized but otherwise unconstrained; use
	// its final ')' rather than splitting the process name on spaces or ')'. The
	// remaining fields begin at state (field 3), so starttime (field 22) is index
	// 19 in this suffix.
	statText := string(stat)
	closeComm := strings.LastIndexByte(statText, ')')
	if closeComm < 0 {
		return procStat{}, fmt.Errorf("enginehost: malformed proc stat: missing comm terminator")
	}
	fields := strings.Fields(statText[closeComm+1:])
	if len(fields) <= 19 {
		return procStat{}, fmt.Errorf("enginehost: malformed proc stat: got %d post-comm fields", len(fields))
	}
	if len(fields[0]) != 1 {
		return procStat{}, fmt.Errorf("enginehost: malformed proc stat state %q", fields[0])
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procStat{}, fmt.Errorf("enginehost: parse proc starttime: %w", err)
	}
	return procStat{state: fields[0][0], startTime: startTime}, nil
}

// execStarter is the production ProcessStarter: it launches the engine-host
// binary with a context-FREE exec.Command (the process is owned by the launcher,
// not by any request context) and the two env vars the JVM reads for its port +
// data root. Display and KCEF bundle settings are inherited from the process
// environment, while TSUNDOKU_ENGINE_KCEF is appended explicitly from the
// profile's resolved capability.
type execStarter struct {
	hostBin string
}

// Start spawns the engine-host binary and captures its Linux /proc starttime.
// The single reaper observes exit without reaping and retains the zombie as a
// PGID pin until a terminal signal has been delivered to the complete group,
// then calls Wait exactly once. Failure to establish identity fails the spawn
// rather than returning an unsafe handle.
func (s execStarter) Start(port int, dataDir string, kcefEnabled bool) (RunningProcess, error) {
	groups := processGroupSystem(linuxProcessGroupSystem{})
	cmd := exec.Command(s.hostBin) //nolint:gosec // hostBin is operator config, not user input
	cmd.Env = buildHostEnv(os.Environ(), port, dataDir, kcefEnabled)
	// Every managed JVM owns a dedicated process group. Chromium descendants
	// inherit it, allowing teardown and capacity accounting to cover the complete
	// browser generation rather than only the JVM leader.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Inherit stdio so the JVM's logs are visible alongside the Go server's (the
	// entrypoint does the same for the default instance).
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exec engine-host %q: %w", s.hostBin, err)
	}
	leaderStartTime, err := groups.LeaderStartTime(cmd.Process.Pid)
	if err != nil {
		// Ownership identity is mandatory: without it a later recycled PGID could be
		// mistaken for this generation. Kill only the exact child PID and perform
		// its sole Wait synchronously; never group-signal an unidentified target.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("exec engine-host %q: capture process identity: %w", s.hostBin, err)
	}

	proc := &execProcess{
		cmd: cmd, pgid: cmd.Process.Pid, leaderStartTime: leaderStartTime,
		groups: groups, done: make(chan struct{}), reaped: make(chan struct{}),
		terminalSignal: make(chan struct{}),
	}
	go proc.reapAfterGroupQuiesces()
	return proc, nil
}

// buildHostEnv appends the per-instance TSUNDOKU_ENGINE_PORT + TSUNDOKU_ENGINE_DATA
// overrides plus an explicit TSUNDOKU_ENGINE_KCEF value onto a copy of base.
// Later entries win in exec's environment, so each managed profile receives its
// derived capability instead of inheriting the default host's value. Extracted
// as a pure helper so the env shape is unit-testable without spawning a process.
func buildHostEnv(base []string, port int, dataDir string, kcefEnabled bool) []string {
	env := make([]string, 0, len(base)+3)
	env = append(env, base...)
	env = append(env,
		fmt.Sprintf("TSUNDOKU_ENGINE_PORT=%d", port),
		"TSUNDOKU_ENGINE_DATA="+dataDir,
		fmt.Sprintf("TSUNDOKU_ENGINE_KCEF=%t", kcefEnabled),
	)
	return env
}

// execProcess is the production RunningProcess wrapping an *exec.Cmd.
type execProcess struct {
	cmd             *exec.Cmd
	pgid            int
	leaderStartTime uint64
	groups          processGroupSystem
	done            chan struct{}
	reaped          chan struct{}
	terminalSignal  chan struct{}
	terminalOnce    sync.Once
	handleMu        sync.Mutex
	waited          bool
}

// Pid returns the OS process id.
func (p *execProcess) Pid() int { return p.cmd.Process.Pid }

// GroupID returns the dedicated process-group id assigned when the JVM started.
func (p *execProcess) GroupID() int { return p.pgid }

// Signal delivers sig while the original leader remains an unreaped PGID pin,
// so numeric reuse cannot retarget the final syscall.
func (p *execProcess) Signal(sig os.Signal) error {
	syscallSignal, ok := sig.(syscall.Signal)
	if !ok {
		return os.ErrInvalid
	}
	return p.signalGroup(syscallSignal)
}

// Kill force-terminates the original managed JVM group under the same pin.
func (p *execProcess) Kill() error { return p.signalGroup(syscall.SIGKILL) }

func (p *execProcess) signalGroup(signal syscall.Signal) error {
	err := p.signalOwnedGroup(signal)
	if signal == syscall.SIGKILL && (err == nil || errors.Is(err, syscall.ESRCH)) {
		p.terminalOnce.Do(func() { close(p.terminalSignal) })
	}
	return err
}

// GroupExists probes the complete original process group. ESRCH or a different
// leader starttime proves the original group absent; permission and unexpected
// failures retain ownership fail-closed.
func (p *execProcess) GroupExists() (bool, error) {
	owned, absent, err := p.groupOwnership()
	if err != nil {
		return true, err
	}
	return owned && !absent, nil
}

func (p *execProcess) signalOwnedGroup(signal syscall.Signal) error {
	// handleMu serializes this complete group syscall against the sole Wait. The
	// running or zombie leader therefore pins the numeric PGID until delivery has
	// completed; if Wait won first, the original group was already quiescent and
	// the numeric value is never touched again.
	p.handleMu.Lock()
	defer p.handleMu.Unlock()
	if p.waited {
		return syscall.ESRCH
	}
	return p.groups.SignalGroup(p.pgid, signal)
}

func (p *execProcess) groupOwnership() (owned bool, absent bool, err error) {
	startTime, identityErr := p.groups.LeaderStartTime(p.pgid)
	switch {
	case identityErr == nil && startTime == p.leaderStartTime:
		return true, false, nil
	case identityErr == nil:
		// The numeric PGID has a different leader generation. Linux cannot reuse
		// it while any member of the original group remains, so the owned group is
		// absent and this new group must never be probed or signalled as ours.
		return false, true, nil
	case !errors.Is(identityErr, os.ErrNotExist):
		return false, false, identityErr
	}

	// Missing leader identity alone cannot prove the group empty: descendants can
	// outlive the leader. A signal-0 group probe is harmless and ESRCH is the
	// kernel's authoritative complete-disappearance result. Other results retain
	// ownership fail-closed. A different leader starttime is handled above and is
	// never probed, so an unrelated recycled group is never touched.
	probeErr := p.groups.SignalGroup(p.pgid, 0)
	switch {
	case probeErr == nil:
		return true, false, nil
	case errors.Is(probeErr, syscall.ESRCH):
		return false, true, nil
	default:
		return true, false, probeErr
	}
}

func (p *execProcess) reapAfterGroupQuiesces() {
	for {
		exited, err := p.groups.LeaderExited(p.pgid)
		if err == nil && exited {
			break
		}
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(groupExitPollInterval)
	}
	close(p.done)

	// /proc enumeration cannot prove group quiescence atomically: an observed
	// member may fork after the snapshot and then disappear. Keep the exited
	// leader as a non-recyclable PGID pin until SIGKILL has been delivered to the
	// whole group. Members cannot fork after that terminal syscall, so exact Wait
	// followed by the kernel's ESRCH group probe is the safe release boundary.
	<-p.terminalSignal
	p.finishReap(func() { _ = p.cmd.Wait() })
	close(p.reaped)
}

func (p *execProcess) finishReap(wait func()) {
	p.handleMu.Lock()
	defer p.handleMu.Unlock()
	wait()
	p.waited = true
}

// Done is closed by the reaper goroutine once leader exit is observed. The
// exact Wait may follow later while the zombie leader pins descendant identity.
func (p *execProcess) Done() <-chan struct{} { return p.done }

func (p *execProcess) Reaped() <-chan struct{} { return p.reaped }
