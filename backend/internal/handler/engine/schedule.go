package engine

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/job"
)

// ScheduleSnapshotter reports the background runner's cycle schedule — whether a
// download cycle / refresh sweep is running right now and when each may next
// start. *job.Runner satisfies it via ScheduleSnapshot. Exported so the route
// wiring can name the port without importing the job package itself.
type ScheduleSnapshotter interface {
	ScheduleSnapshot() job.Schedule
}

// WithSchedule attaches the cycle-schedule read port that GET /api/engine/schedule
// serves. Returns the receiver for chaining off NewHandler; every other endpoint
// here ignores it, so it stays nil for the constructor's plain call sites.
func (h *Handler) WithSchedule(s ScheduleSnapshotter) *Handler {
	h.schedule = s
	return h
}

// Schedule handles GET /api/engine/schedule. It returns the live cadence state of
// the two background loops: is a download cycle running, is a refresh sweep
// running, and the earliest instant each may next start.
//
// It is a pure in-memory read of the runner's own snapshot — ZERO DB queries and
// ZERO engine calls — so a client may poll it freely. It exists because the state
// is otherwise unobservable: a client that connects mid-cycle sees no SSE
// boundary event and would have to guess (the countdowns and the idle/running pill
// were previously fabricated client-side, showing "Idle" while downloads ran).
//
// The next-run instants may be IN THE PAST — see ScheduleDTO for the contract.
func (h *Handler) Schedule(c echo.Context) error {
	return c.JSON(http.StatusOK, toScheduleDTO(h.schedule.ScheduleSnapshot(), time.Now()))
}
