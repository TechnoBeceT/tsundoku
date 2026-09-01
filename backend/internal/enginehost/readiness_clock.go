package enginehost

import "time"

// ReadinessTimer is the one-shot clock handle used by the coordinated launch
// readiness budget. It is deliberately narrower than time.Timer so tests can
// advance every readiness phase without waiting on wall time.
type ReadinessTimer interface {
	C() <-chan time.Time
	Stop() bool
}

// ReadinessTicker drives bounded readiness polling under the same clock as the
// absolute launch deadline.
type ReadinessTicker interface {
	C() <-chan time.Time
	Stop()
}

// ReadinessClock supplies the monotonic time source for launch readiness. The
// production clock delegates to time; tests use a virtual clock to prove that
// no phase receives a fresh budget.
type ReadinessClock interface {
	Now() time.Time
	NewTimer(time.Duration) ReadinessTimer
	NewTicker(time.Duration) ReadinessTicker
}

type systemReadinessClock struct{}

func (systemReadinessClock) Now() time.Time { return time.Now() }

func (systemReadinessClock) NewTimer(d time.Duration) ReadinessTimer {
	return systemReadinessTimer{Timer: time.NewTimer(d)}
}

func (systemReadinessClock) NewTicker(d time.Duration) ReadinessTicker {
	return systemReadinessTicker{Ticker: time.NewTicker(d)}
}

type systemReadinessTimer struct{ *time.Timer }

func (t systemReadinessTimer) C() <-chan time.Time { return t.Timer.C }

type systemReadinessTicker struct{ *time.Ticker }

func (t systemReadinessTicker) C() <-chan time.Time { return t.Ticker.C }
