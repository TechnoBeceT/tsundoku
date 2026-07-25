package engine_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	handler "github.com/technobecet/tsundoku/internal/handler/engine"
	"github.com/technobecet/tsundoku/internal/job"
	"github.com/technobecet/tsundoku/internal/middleware"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

// fakeSchedule is a stub cycle-schedule port returning a fixed snapshot, so the
// endpoint's mapping can be asserted without running a background loop.
type fakeSchedule struct {
	snap job.Schedule
}

// ScheduleSnapshot returns the fixed snapshot.
func (f fakeSchedule) ScheduleSnapshot() job.Schedule { return f.snap }

// scheduleEnv wires an Echo instance with GET /api/engine/schedule behind
// RequireOwner (so the 401 proof hits the real middleware) over a stub schedule
// port. The endpoint reads NOTHING but that port — no APK cache, no DB — so both
// are deliberately nil here; a nil dereference would be a regression, not a
// missing fixture.
type scheduleEnv struct {
	e     *echo.Echo
	token string
}

func newScheduleEnv(t *testing.T, snap job.Schedule) *scheduleEnv {
	t.Helper()
	authSvc := auth.NewService(testSecret)
	h := handler.NewHandler(nil, nil).WithSchedule(fakeSchedule{snap: snap})

	e := echo.New()
	e.HTTPErrorHandler = middleware.ErrorHandler
	authed := e.Group("/api", middleware.RequireOwner(authSvc, false))
	authed.GET("/engine/schedule", h.Schedule)

	token, err := authSvc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue token: %v", err)
	}
	return &scheduleEnv{e: e, token: token}
}

// get performs the request, optionally authenticated.
func (env *scheduleEnv) get(t *testing.T, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/engine/schedule", nil)
	if withAuth {
		r.Header.Set("Authorization", "Bearer "+env.token)
	}
	rec := httptest.NewRecorder()
	env.e.ServeHTTP(rec, r)
	return rec
}

// decodeSchedule asserts a 200 and decodes the schedule DTO.
func decodeSchedule(t *testing.T, rec *httptest.ResponseRecorder) handler.ScheduleDTO {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var dto handler.ScheduleDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return dto
}

// TestSchedule_Unauthorized proves the route is behind RequireOwner.
func TestSchedule_Unauthorized(t *testing.T) {
	env := newScheduleEnv(t, job.Schedule{})
	rec := env.get(t, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", rec.Code)
	}
}

// TestSchedule_UnscheduledLoopsSerializeNull proves a runner with no loop running
// reports null next-run instants and no overdue claim — "nothing is planned" must
// not be dressed up as a due cycle.
func TestSchedule_UnscheduledLoopsSerializeNull(t *testing.T) {
	env := newScheduleEnv(t, job.Schedule{})
	rec := env.get(t, true)
	dto := decodeSchedule(t, rec)

	if body := rec.Body.String(); !strings.Contains(body, `"nextRunAt":null`) {
		t.Errorf("body %s: want an explicit \"nextRunAt\":null", body)
	}
	for name, loop := range map[string]handler.CycleScheduleDTO{"download": dto.Download, "refresh": dto.Refresh} {
		if loop.Running || loop.NextRunAt != nil || loop.Overdue {
			t.Errorf("%s = %+v, want not running, null next run, not overdue", name, loop)
		}
	}
	if dto.ServerTime.IsZero() {
		t.Error("serverTime is zero — the client needs it to compute a skew-free countdown")
	}
}

// TestSchedule_FutureNextRunIsNotOverdue proves a normally-scheduled loop reports
// its next-run instant verbatim and is not flagged overdue.
func TestSchedule_FutureNextRunIsNotOverdue(t *testing.T) {
	next := time.Now().Add(90 * time.Second).UTC()
	env := newScheduleEnv(t, job.Schedule{
		Download: job.LoopSchedule{NextRunAt: next},
		Refresh:  job.LoopSchedule{NextRunAt: next.Add(time.Hour)},
	})
	dto := decodeSchedule(t, env.get(t, true))

	if dto.Download.NextRunAt == nil || !dto.Download.NextRunAt.Equal(next) {
		t.Fatalf("download.nextRunAt = %v, want %v", dto.Download.NextRunAt, next)
	}
	if dto.Download.Overdue || dto.Refresh.Overdue {
		t.Errorf("overdue = %v/%v, want false for both future next-runs", dto.Download.Overdue, dto.Refresh.Overdue)
	}
}

// TestSchedule_RunningOverrunReportsOverdue is the honesty contract: when a cycle
// overruns its period, the next-run instant it published is already in the past.
// The endpoint reports that instant as-is and flags overdue — it never invents a
// future timestamp to keep a countdown looking tidy.
func TestSchedule_RunningOverrunReportsOverdue(t *testing.T) {
	past := time.Now().Add(-23 * time.Second).UTC()
	env := newScheduleEnv(t, job.Schedule{
		Download: job.LoopSchedule{Running: true, NextRunAt: past},
	})
	dto := decodeSchedule(t, env.get(t, true))

	if !dto.Download.Running {
		t.Error("download.running = false, want true")
	}
	if dto.Download.NextRunAt == nil || !dto.Download.NextRunAt.Equal(past) {
		t.Fatalf("download.nextRunAt = %v, want the already-past %v reported verbatim", dto.Download.NextRunAt, past)
	}
	if !dto.Download.Overdue {
		t.Error("download.overdue = false, want true for a next-run instant that has passed")
	}
	if dto.Download.NextRunAt.After(dto.ServerTime) {
		t.Error("nextRunAt is after serverTime — an overdue cycle must read as due against the server's own clock")
	}
}
