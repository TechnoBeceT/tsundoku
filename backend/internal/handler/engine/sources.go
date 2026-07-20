package engine

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/sourcegate"
)

// activeSourceCounter reports how many chapters are currently being fetched from
// each source, keyed by canonical source name. *downloads.Service satisfies it via
// ActiveSourceCounts. A narrow port so this handler never pulls in the whole
// downloads package surface.
type activeSourceCounter interface {
	ActiveSourceCounts(ctx context.Context) (map[string]int, error)
}

// breakerSnapshotter is the one-shot circuit-breaker snapshot, keyed by the SAME
// canonical source name. *sourcegate.Service satisfies it via Snapshot.
type breakerSnapshotter interface {
	Snapshot(ctx context.Context) (map[string]sourcegate.BreakerState, error)
}

// downloadConcurrencyProvider reports the current per-source download-concurrency
// cap (the "N/cap" denominator), read at use-time so an owner's settings change
// applies immediately. *settings.Service and settings.Static both satisfy it.
type downloadConcurrencyProvider interface {
	DownloadConcurrency(ctx context.Context) int
}

// WithSourceStatus attaches the three read ports the live source-status strip
// (GET /api/engine/sources) needs: the ACTIVE per-source download counts, the
// circuit-breaker snapshot (the cooling sources), and the concurrency cap. Returns
// the receiver for chaining off NewHandler; the topology-status/apk endpoints do
// not use them, so they stay nil for the constructor's existing call sites.
func (h *Handler) WithSourceStatus(counts activeSourceCounter, breakers breakerSnapshotter, concurrency downloadConcurrencyProvider) *Handler {
	h.activeCounts = counts
	h.breakers = breakers
	h.concurrency = concurrency
	return h
}

// Sources handles GET /api/engine/sources. It returns the live per-source status
// strip: exactly the sources DOING something right now — actively downloading
// (≥1 chapter in flight) or in an anti-ban cooldown — omitting the fully-idle
// majority so the strip never becomes a 40-row wall.
//
// It is a pure DB + in-memory read: the ACTIVE counts come from the download
// read-model (chapters in downloading/upgrading, attributed to their fetching
// source, no-N+1), the COOLING half from the persisted circuit-breaker snapshot.
// It makes ZERO calls to the engine/sources — an observability endpoint must never
// itself hammer the sources it reports on.
func (h *Handler) Sources(c echo.Context) error {
	ctx := c.Request().Context()

	active, err := h.activeCounts.ActiveSourceCounts(ctx)
	if err != nil {
		// A DB read failure is a genuine server error → central middleware 500.
		return err
	}
	breakers, err := h.breakers.Snapshot(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, buildSourceStatuses(active, breakers, h.concurrency.DownloadConcurrency(ctx), time.Now()))
}
