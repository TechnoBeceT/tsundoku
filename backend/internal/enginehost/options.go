package enginehost

import "time"

// Option customizes a Launcher at construction. In production New needs no
// options (it wires the real seams); the With* options exist so unit tests can
// inject fake process/health/port seams and shrink the lifecycle timers, driving
// the whole launcher with no real process and no real network.
type Option func(*Launcher)

// WithStarter replaces the process-spawn seam (default: a real exec.Command
// starter). Tests pass a fake that records the port/dataDir and returns an
// in-memory process.
func WithStarter(s ProcessStarter) Option { return func(l *Launcher) { l.starter = s } }

// WithHealthProber replaces the readiness/liveness prober (default: an HTTP GET
// /health). Tests pass a deterministic function.
func WithHealthProber(p HealthProber) Option { return func(l *Launcher) { l.prober = p } }

// WithPortAllocator replaces the free-port allocator (default: bind 127.0.0.1:0).
// Tests pass a deterministic allocator.
func WithPortAllocator(a PortAllocator) Option { return func(l *Launcher) { l.allocPort = a } }

// WithStartTimeout sets how long a spawn waits for the first healthy /health
// before killing the process and failing (default 60s). Tests shrink it to keep
// the timeout path fast.
func WithStartTimeout(d time.Duration) Option { return func(l *Launcher) { l.startTimeout = d } }

// WithPollInterval sets the gap between health polls during a spawn (default
// 500ms). Tests shrink it.
func WithPollInterval(d time.Duration) Option { return func(l *Launcher) { l.pollInterval = d } }

// WithStopGrace sets the SIGTERM→SIGKILL grace period on stop (default 5s). Tests
// shrink it to keep the kill-escalation path fast.
func WithStopGrace(d time.Duration) Option { return func(l *Launcher) { l.stopGrace = d } }
