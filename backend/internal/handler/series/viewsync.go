package series

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// ViewSyncer is the narrow port Detail uses to fire the tracker-sync
// detail-open reconcile — satisfied by syncsvc.Service.SyncOnView. Depending
// on this narrow interface (rather than importing internal/tracker/syncsvc
// directly) keeps the handler package free of the tracker subsystem and
// mirrors series.ProgressPusher's exact shape (see that type's doc comment
// for the same layering rationale — an ent-touching orchestration service is
// consumed structurally by whatever triggers it, never imported by name).
//
// SyncOnView owns the whole converge + the ungated-by-auto_update_track
// decision itself — Detail does not need to know any of that; it only
// decides WHETHER to fire the hook at all (see fireSyncOnView below).
type ViewSyncer interface {
	SyncOnView(ctx context.Context, seriesID uuid.UUID) error
}

// WithViewSyncer attaches the tracker-sync detail-open hook and returns the
// handler, so production wires it fluently onto the constructor (mirrors
// series.WithProgressPusher / series.WithCoverFetcher). It is OPTIONAL: a
// Handler with no ViewSyncer attached (the default — every existing
// NewHandler call site, including every pre-existing handler/series test)
// fires no tracker sync on Detail.
func (h *Handler) WithViewSyncer(v ViewSyncer) *Handler {
	h.viewSyncer = v
	return h
}

// fireSyncOnView launches the DETACHED, best-effort tracker-sync reconcile
// for a series whose detail page was just opened. Detail's HTTP response
// must never wait on it — the goroutine runs on context.WithoutCancel(ctx)
// (mirrors series.firePushProgress) so it survives the request context being
// cancelled the instant the handler returns the response, and the detail
// page must render instantly even when a bound tracker is slow or
// unreachable.
//
// A nil viewSyncer is a silent no-op (the common case in every test and any
// deployment with no trackers bound — see WithViewSyncer). Any sync failure
// is logged at WARN, never surfaced — detail-open tracker sync is
// best-effort by design, the same posture as the reading-triggered push.
func (h *Handler) fireSyncOnView(ctx context.Context, seriesID uuid.UUID) {
	if h.viewSyncer == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := h.viewSyncer.SyncOnView(detached, seriesID); err != nil {
			slog.WarnContext(detached, "handler/series: tracker sync-on-view failed", "series_id", seriesID, "err", err)
		}
	}()
}
