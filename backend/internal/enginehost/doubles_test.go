package enginehost_test

import (
	"os"
	"sync"

	"github.com/technobecet/tsundoku/internal/enginehost"
	"github.com/technobecet/tsundoku/internal/engineroute"
	"github.com/technobecet/tsundoku/internal/sourceengine"
	sourceenginefake "github.com/technobecet/tsundoku/internal/sourceengine/fake"
)

// fakeProcess is an in-memory RunningProcess: it records the signals/kill it
// receives and closes its Done channel either when it is killed, or (when
// closeOnSignal is set) when it is first signalled — modelling a JVM that exits
// cleanly on SIGTERM vs one that has to be SIGKILLed.
type fakeProcess struct {
	id            int
	closeOnSignal bool

	mu      sync.Mutex
	signals []os.Signal
	killed  bool

	done     chan struct{}
	doneOnce sync.Once
}

func newFakeProcess(id int, closeOnSignal bool) *fakeProcess {
	return &fakeProcess{id: id, closeOnSignal: closeOnSignal, done: make(chan struct{})}
}

func (p *fakeProcess) Pid() int { return p.id }

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, sig)
	closeNow := p.closeOnSignal
	p.mu.Unlock()
	if closeNow {
		p.exit()
	}
	return nil
}

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.killed = true
	p.mu.Unlock()
	p.exit()
	return nil
}

func (p *fakeProcess) Done() <-chan struct{} { return p.done }

// exit closes Done exactly once (idempotent — Kill after a graceful exit is safe).
func (p *fakeProcess) exit() { p.doneOnce.Do(func() { close(p.done) }) }

// wasKilled reports whether Kill was ever called.
func (p *fakeProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killed
}

// wasSignalled reports whether at least one Signal was delivered.
func (p *fakeProcess) wasSignalled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.signals) > 0
}

// startCall records one ProcessStarter.Start invocation.
type startCall struct {
	port        int
	dataDir     string
	disableKCEF bool
}

// fakeStarter is an in-memory ProcessStarter recording every Start and handing
// back a fresh fakeProcess (closeOnSignal governs whether those procs exit on
// SIGTERM). Set err to make Start fail.
type fakeStarter struct {
	closeOnSignal bool
	err           error

	mu    sync.Mutex
	calls []startCall
	procs []*fakeProcess
}

func (s *fakeStarter) Start(port int, dataDir string, disableKCEF bool) (enginehost.RunningProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	s.calls = append(s.calls, startCall{port: port, dataDir: dataDir, disableKCEF: disableKCEF})
	p := newFakeProcess(len(s.procs)+1, s.closeOnSignal)
	s.procs = append(s.procs, p)
	return p, nil
}

func (s *fakeStarter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *fakeStarter) lastCall() startCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

func (s *fakeStarter) proc(i int) *fakeProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procs[i]
}

// okProber is a HealthProber that always reports ready.
func okProber(string) error { return nil }

// sequenceProber returns the i-th error for the i-th call (clamped to the last),
// so a test can script the exact ready/unready outcomes across a spawn +
// liveness-check + respawn sequence.
func sequenceProber(errs ...error) enginehost.HealthProber {
	var mu sync.Mutex
	i := 0
	return func(string) error {
		mu.Lock()
		defer mu.Unlock()
		e := errs[i]
		if i < len(errs)-1 {
			i++
		}
		return e
	}
}

// fixedPortAllocator hands out ports from a fixed list in order (clamped to the
// last), so a test knows exactly which port each spawn gets.
func fixedPortAllocator(ports ...int) enginehost.PortAllocator {
	var mu sync.Mutex
	i := 0
	return func() (int, error) {
		mu.Lock()
		defer mu.Unlock()
		p := ports[i]
		if i < len(ports)-1 {
			i++
		}
		return p, nil
	}
}

// recordingFactory is an engineroute.ClientFactory that records the base URLs it
// built clients for and returns a distinct fake per URL.
type recordingFactory struct {
	mu   sync.Mutex
	urls []string
}

func (f *recordingFactory) build(baseURL string) sourceengine.Client {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls = append(f.urls, baseURL)
	return sourceenginefake.New()
}

func (f *recordingFactory) factory() engineroute.ClientFactory { return f.build }

// profile is a tiny helper to build a non-default engineroute.Profile with a key.
func profile(key string) engineroute.Profile { return engineroute.Profile{Key: key} }
