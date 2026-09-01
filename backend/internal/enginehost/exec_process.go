package enginehost

import (
	"fmt"
	"os"
	"os/exec"
)

// execStarter is the production ProcessStarter: it launches the engine-host
// binary with a context-FREE exec.Command (the process is owned by the launcher,
// not by any request context) and the two env vars the JVM reads for its port +
// data root. Display and KCEF bundle settings are inherited from the process
// environment, while TSUNDOKU_ENGINE_KCEF is appended explicitly from the
// profile's resolved capability.
type execStarter struct {
	hostBin string
}

// Start spawns the engine-host binary and returns a handle to it. The single
// reaper goroutine calls Wait exactly once and closes the done channel, so the
// process never zombies.
func (s execStarter) Start(port int, dataDir string, kcefEnabled bool) (RunningProcess, error) {
	cmd := exec.Command(s.hostBin) //nolint:gosec // hostBin is operator config, not user input
	cmd.Env = buildHostEnv(os.Environ(), port, dataDir, kcefEnabled)
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
