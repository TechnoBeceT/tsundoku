package engine

import (
	"time"

	"github.com/technobecet/tsundoku/internal/job"
)

// ScheduleDTO is the JSON shape of GET /api/engine/schedule: the live cadence
// state of the two background loops, plus the server's own clock so a client can
// render a countdown without trusting the browser's clock to agree.
type ScheduleDTO struct {
	// Download is the download/upgrade cycle loop.
	Download CycleScheduleDTO `json:"download"`
	// Refresh is the discovery sweep loop.
	Refresh CycleScheduleDTO `json:"refresh"`
	// ServerTime is the instant this snapshot was taken, by the server's clock.
	// Compute a countdown as nextRunAt - serverTime (never as nextRunAt - Date.now()).
	ServerTime time.Time `json:"serverTime"`
}

// CycleScheduleDTO is one loop's schedule.
//
// # The contract for nextRunAt while a cycle is running
//
// nextRunAt is the EARLIEST instant the next cycle may start — the start of the
// most recent cycle plus the configured interval — because the interval is a true
// PERIOD measured from the previous cycle's start, not a gap after it ends.
//
// That instant can therefore already have PASSED while running is true: it is
// what an overrunning cycle looks like (e.g. a 90s period with a 113s cycle). The
// endpoint reports it honestly instead of inventing a future timestamp, and sets
// overdue so a client can render "due now" rather than a negative countdown. The
// missed tick is never queued — exactly one cycle starts as soon as the current
// one returns.
//
// nextRunAt is null when the loop is not scheduled at all: it was never started,
// or its context was cancelled and the goroutine has exited.
type CycleScheduleDTO struct {
	// Running reports whether a cycle is executing right now. Cycles never
	// overlap, so this is also "a tick landing now would be skipped".
	Running bool `json:"running"`
	// NextRunAt is the earliest instant the next cycle may start; null when the
	// loop is unscheduled. May be in the past — see the type doc.
	NextRunAt *time.Time `json:"nextRunAt"`
	// Overdue is true when nextRunAt is known and has already passed: the next
	// cycle is due (it starts the moment the current one finishes, if any).
	Overdue bool `json:"overdue"`
}

// toScheduleDTO maps the runner's schedule snapshot onto the wire DTO, resolving
// each loop's overdue flag against now (the same instant reported as serverTime,
// so overdue and the client's own countdown can never disagree).
func toScheduleDTO(s job.Schedule, now time.Time) ScheduleDTO {
	return ScheduleDTO{
		Download:   toCycleScheduleDTO(s.Download, now),
		Refresh:    toCycleScheduleDTO(s.Refresh, now),
		ServerTime: now,
	}
}

// toCycleScheduleDTO maps one loop's schedule. An unscheduled loop (zero
// NextRunAt) serializes nextRunAt as null and is never overdue — "no next run is
// planned" is not the same claim as "the next run is late".
func toCycleScheduleDTO(l job.LoopSchedule, now time.Time) CycleScheduleDTO {
	if l.NextRunAt.IsZero() {
		return CycleScheduleDTO{Running: l.Running}
	}
	next := l.NextRunAt
	return CycleScheduleDTO{
		Running:   l.Running,
		NextRunAt: &next,
		Overdue:   !next.After(now),
	}
}
