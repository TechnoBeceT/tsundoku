package enginehost_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

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

	mu       sync.Mutex
	signals  []os.Signal
	killed   bool
	onSignal func()

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
	onSignal := p.onSignal
	p.mu.Unlock()
	if onSignal != nil {
		onSignal()
	}
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

func (p *fakeProcess) setOnSignal(fn func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onSignal = fn
}

// startCall records one ProcessStarter.Start invocation.
type startCall struct {
	port        int
	dataDir     string
	kcefEnabled bool
}

// fakeStarter is an in-memory ProcessStarter recording every Start and handing
// back a fresh fakeProcess (closeOnSignal governs whether those procs exit on
// SIGTERM). Set err to make Start fail.
type fakeStarter struct {
	closeOnSignal bool
	err           error

	mu       sync.Mutex
	attempts int // every Start call, including ones that fail (err set)
	calls    []startCall
	procs    []*fakeProcess
}

func (s *fakeStarter) Start(port int, dataDir string, kcefEnabled bool) (enginehost.RunningProcess, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.err != nil {
		return nil, s.err
	}
	s.calls = append(s.calls, startCall{port: port, dataDir: dataDir, kcefEnabled: kcefEnabled})
	p := newFakeProcess(len(s.procs)+1, s.closeOnSignal)
	s.procs = append(s.procs, p)
	return p, nil
}

func (s *fakeStarter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// attemptCount returns the number of Start invocations INCLUDING failed ones, so
// a supervisor test can count restart attempts even when the starter errors.
func (s *fakeStarter) attemptCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// setErr toggles the Start error at runtime, so a test can let the initial spawn
// succeed and then make subsequent restarts fail (or recover).
func (s *fakeStarter) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
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

// readyKCEFStatus is the normal operational snapshot for tests that do not
// exercise the status contract itself. It keeps launch tests focused on their
// declared health/process behavior while still mirroring the required RPC shape.
func readyKCEFStatus() enginehost.EngineStatus {
	return enginehost.EngineStatus{KCEF: enginehost.KCEFStatus{State: enginehost.KCEFStateReady}}
}

func disabledKCEFStatus() enginehost.EngineStatus {
	return enginehost.EngineStatus{KCEF: enginehost.KCEFStatus{State: enginehost.KCEFStateDisabled}}
}

func kcefError(code enginehost.KCEFErrorCode) *enginehost.KCEFErrorCode { return &code }

type fakeReadinessClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  []*fakeReadinessTimer
	tickers []*fakeReadinessTicker
}

type fakeReadinessTimer struct {
	mu       sync.Mutex
	deadline time.Time
	channel  chan time.Time
	stopped  bool
	fired    bool
}

func (t *fakeReadinessTimer) C() <-chan time.Time { return t.channel }

func (t *fakeReadinessTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

type fakeReadinessTicker struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
	channel  chan time.Time
	stopped  bool
}

func (t *fakeReadinessTicker) C() <-chan time.Time { return t.channel }

func (t *fakeReadinessTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}

func newFakeReadinessClock(now time.Time) *fakeReadinessClock { return &fakeReadinessClock{now: now} }

func (c *fakeReadinessClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeReadinessClock) NewTimer(d time.Duration) enginehost.ReadinessTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeReadinessTimer{deadline: c.now.Add(d), channel: make(chan time.Time, 1)}
	c.timers = append(c.timers, timer)
	return timer
}

func (c *fakeReadinessClock) NewTicker(d time.Duration) enginehost.ReadinessTicker {
	c.mu.Lock()
	defer c.mu.Unlock()
	ticker := &fakeReadinessTicker{next: c.now.Add(d), interval: d, channel: make(chan time.Time, 1)}
	c.tickers = append(c.tickers, ticker)
	return ticker
}

func (c *fakeReadinessClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for _, timer := range c.timers {
		timer.mu.Lock()
		if !timer.stopped && !timer.fired && !timer.deadline.After(c.now) {
			timer.fired = true
			timer.channel <- c.now
		}
		timer.mu.Unlock()
	}
	for _, ticker := range c.tickers {
		ticker.mu.Lock()
		if ticker.stopped || ticker.next.After(c.now) {
			ticker.mu.Unlock()
			continue
		}
		select {
		case ticker.channel <- c.now:
		default:
		}
		for !ticker.next.After(c.now) {
			ticker.next = ticker.next.Add(ticker.interval)
		}
		ticker.mu.Unlock()
	}
}

func (c *fakeReadinessClock) waitForTicker(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		count := len(c.tickers)
		c.mu.Unlock()
		if count > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("readiness poll did not create a ticker")
		}
		time.Sleep(time.Millisecond)
	}
}

func sequenceStatus(statuses ...enginehost.EngineStatus) enginehost.StatusProber {
	var mu sync.Mutex
	i := 0
	return func(context.Context, string) (enginehost.EngineStatus, error) {
		mu.Lock()
		defer mu.Unlock()
		status := statuses[i]
		if i < len(statuses)-1 {
			i++
		}
		return status, nil
	}
}

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
func profile(key string) engineroute.Profile { return engineroute.Profile{Key: key, KCEFEnabled: true} }

// profileWithSources builds a non-default profile carrying the given source ids,
// so a supervisor test can assert the degrade/restore overlay moves exactly those
// sources.
func profileWithSources(key string, ids ...int64) engineroute.Profile {
	return engineroute.Profile{Key: key, SourceIDs: ids, KCEFEnabled: true}
}

// fakeRerouter is an in-memory enginehost.Rerouter recording the degrade/restore
// calls so a supervisor test can assert which sources were moved to/from the
// default engine, and track the net currently-degraded set.
type fakeRerouter struct {
	mu           sync.Mutex
	degradeCalls int
	restoreCalls int
	degraded     map[int64]bool
	events       []rerouteEvent
}

type rerouteEvent struct {
	kind string
	ids  []int64
}

func newFakeRerouter() *fakeRerouter { return &fakeRerouter{degraded: map[int64]bool{}} }

func (r *fakeRerouter) Degrade(ids []int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.degradeCalls++
	r.events = append(r.events, rerouteEvent{kind: "degrade", ids: append([]int64(nil), ids...)})
	for _, id := range ids {
		r.degraded[id] = true
	}
}

func (r *fakeRerouter) Restore(ids []int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreCalls++
	r.events = append(r.events, rerouteEvent{kind: "restore", ids: append([]int64(nil), ids...)})
	for _, id := range ids {
		delete(r.degraded, id)
	}
}

// isDegraded reports whether id is currently in the net degraded set.
func (r *fakeRerouter) isDegraded(id int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.degraded[id]
}

// counts returns the number of Degrade / Restore calls received.
func (r *fakeRerouter) counts() (degrade, restore int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.degradeCalls, r.restoreCalls
}

func (r *fakeRerouter) resetEvents() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

func (r *fakeRerouter) recordedEvents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]string, len(r.events))
	for i, event := range r.events {
		events[i] = event.kind
	}
	return events
}

func (r *fakeRerouter) recordedEventsFor(id int64) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var events []string
	for _, event := range r.events {
		for _, eventID := range event.ids {
			if eventID == id {
				events = append(events, event.kind)
				break
			}
		}
	}
	return events
}
