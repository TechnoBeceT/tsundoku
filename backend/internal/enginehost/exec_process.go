package enginehost

import (
	"fmt"
	"os"
	"os/exec"
)

// execStarter is the production ProcessStarter: it launches the engine-host
// binary with a context-FREE exec.Command (the process is owned by the launcher,
// not by any request context) and the two env vars the JVM reads for its port +
// data root. Every other var the JVM needs (DISPLAY, dbus address,
// TSUNDOKU_ENGINE_KCEF, ENGINE_KCEF_BUNDLE) is INHERITED from this process's
// environment — the same environment the container entrypoint set up for the
// default instance — so a launched profile behaves identically to it.
type execStarter struct {
	hostBin string
}

// Start spawns the engine-host binary and returns a handle to it. The single
// reaper goroutine calls Wait exactly once and closes the done channel, so the
// process never zombies.
func (s execStarter) Start(port int, dataDir string) (RunningProcess, error) {
	cmd := exec.Command(s.hostBin) //nolint:gosec // hostBin is operator config, not user input
	cmd.Env = buildHostEnv(os.Environ(), port, dataDir)
	// Inherit stdio so the JVM's logs are visible alongside the Go server's (the
	// entrypoint does the same for the default instance).
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("exec engine-host %q: %w", s.hostBin, err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return &execProcess{cmd: cmd, done: done}, nil
}

// buildHostEnv appends the per-instance TSUNDOKU_ENGINE_PORT + TSUNDOKU_ENGINE_DATA
// overrides onto a copy of base. Later entries win in exec's env, so these
// override any inherited value — a launched profile MUST NOT share the default
// instance's port (7777) or data dir. Extracted as a pure helper so the env shape
// is unit-testable without spawning a process.
func buildHostEnv(base []string, port int, dataDir string) []string {
	env := make([]string, 0, len(base)+2)
	env = append(env, base...)
	env = append(env,
		fmt.Sprintf("TSUNDOKU_ENGINE_PORT=%d", port),
		"TSUNDOKU_ENGINE_DATA="+dataDir,
	)
	return env
}

// execProcess is the production RunningProcess wrapping an *exec.Cmd.
type execProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// Pid returns the OS process id.
func (p *execProcess) Pid() int { return p.cmd.Process.Pid }

// Signal delivers sig to the process.
func (p *execProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }

// Kill force-terminates the process.
func (p *execProcess) Kill() error { return p.cmd.Process.Kill() }

// Done is closed by the reaper goroutine once the process has exited.
func (p *execProcess) Done() <-chan struct{} { return p.done }
