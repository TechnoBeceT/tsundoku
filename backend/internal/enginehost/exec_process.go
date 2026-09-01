package enginehost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

type processGroupSystem interface {
	LeaderStartTime(pid int) (uint64, error)
	SignalGroup(pgid int, signal syscall.Signal) error
}

type linuxProcessGroupSystem struct{}

func (linuxProcessGroupSystem) LeaderStartTime(pid int) (uint64, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) //nolint:gosec // pid is the just-spawned owned child
	if err != nil {
		return 0, err
	}
	return parseProcStartTime(stat)
}

func (linuxProcessGroupSystem) SignalGroup(pgid int, signal syscall.Signal) error {
	return syscall.Kill(-pgid, signal)
}

func parseProcStartTime(stat []byte) (uint64, error) {
	// proc_pid_stat(5) makes comm parenthesized but otherwise unconstrained; use
	// its final ')' rather than splitting the process name on spaces or ')'. The
	// remaining fields begin at state (field 3), so starttime (field 22) is index
	// 19 in this suffix.
	statText := string(stat)
	closeComm := strings.LastIndexByte(statText, ')')
	if closeComm < 0 {
		return 0, fmt.Errorf("enginehost: malformed proc stat: missing comm terminator")
	}
	fields := strings.Fields(statText[closeComm+1:])
	if len(fields) <= 19 {
		return 0, fmt.Errorf("enginehost: malformed proc stat: got %d post-comm fields", len(fields))
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("enginehost: parse proc starttime: %w", err)
	}
	return startTime, nil
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

// Start spawns the engine-host binary, captures its Linux /proc starttime as the
// non-recyclable group ownership token, and returns a handle to it. The single
// reaper goroutine calls Wait exactly once and closes the done channel, so the
// process never zombies. Failure to capture identity fails the spawn rather than
// returning a handle that could later signal a recycled PGID.
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

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return &execProcess{
		cmd: cmd, pgid: cmd.Process.Pid, leaderStartTime: leaderStartTime, groups: groups, done: done,
	}, nil
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
}

// Pid returns the OS process id.
func (p *execProcess) Pid() int { return p.cmd.Process.Pid }

// GroupID returns the dedicated process-group id assigned when the JVM started.
func (p *execProcess) GroupID() int { return p.pgid }

// Signal revalidates the captured leader identity and delivers sig only to the
// original managed JVM group.
func (p *execProcess) Signal(sig os.Signal) error {
	syscallSignal, ok := sig.(syscall.Signal)
	if !ok {
		return os.ErrInvalid
	}
	return p.signalOwnedGroup(syscallSignal)
}

// Kill force-terminates the original managed JVM group after the same identity
// validation.
func (p *execProcess) Kill() error { return p.signalOwnedGroup(syscall.SIGKILL) }

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
	// Revalidate inside the signal operation even if a caller just probed. The
	// second check also closes the ordinary leader-exit/recycle window between a
	// descendant-only probe and the non-zero group signal.
	for range 2 {
		owned, absent, err := p.groupOwnership()
		if err != nil {
			return err
		}
		if absent || !owned {
			return syscall.ESRCH
		}
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

	// The original leader has been reaped. A still-existing PGID can only be its
	// descendants; once those disappear, kill(-pgid, 0) returns ESRCH. A later
	// recycled leader is detected by the identity read above.
	probeErr := p.groups.SignalGroup(p.pgid, 0)
	switch {
	case probeErr == nil:
		return true, false, nil
	case errors.Is(probeErr, syscall.ESRCH):
		return false, true, nil
	default:
		return false, false, probeErr
	}
}

// Done is closed by the reaper goroutine once the process has exited.
func (p *execProcess) Done() <-chan struct{} { return p.done }
