package series

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
)

// ProgressPusher is the narrow port SetProgress uses to fire the tracker-sync
// progress push after a chapter is marked read from the in-app reader
// (spec/trackers-sync-phase4 §2, trigger (a)) — satisfied by
// syncsvc.Service.PushProgress. Depending on this narrow interface (rather
// than importing internal/tracker/syncsvc directly) keeps the series domain
// free of the tracker subsystem and mirrors internal/imports.AutoIdentifier's
// exact shape (see that package's doc comment for the layering rationale: an
// ent-touching orchestration service sits ABOVE the domain it hooks into,
// never below — a series→tracker import would invert the dependency the
// same way a series→imports edge would for the chapterrange kernel).
//
// PushProgress owns the auto_update_track gate and the never-regress/
// truncate/auto-complete decisions itself — SetProgress's caller does not
// need to know any of that; it only decides WHETHER to fire the hook at all
// (see firePushProgress below).
type ProgressPusher interface {
	PushProgress(ctx context.Context, seriesID uuid.UUID, localFurthest float64) error
}

// WithProgressPusher attaches the tracker-sync progress-push hook and
// returns the service, so production wires it fluently onto the constructor
// (mirrors WithCoverFetcher). It is OPTIONAL: a Service with no
// ProgressPusher attached (the default — every existing NewService /
// NewServiceWithStaleGrace call site, including every pre-existing
// series/reader test) fires no tracker push on SetProgress.
func (s *Service) WithProgressPusher(p ProgressPusher) *Service {
	s.progressPusher = p
	return s
}

// firePushProgress launches the DETACHED, best-effort tracker-sync push for
// a chapter just marked read from the reader (spec/trackers-sync-phase4
// §2's reading-triggered trigger). SetProgress's HTTP response must never
// wait on it — the goroutine runs on context.WithoutCancel(ctx) (mirrors
// imports.fireAutoIdentify) so it survives the request context being
// cancelled the instant the handler returns.
//
// A nil progressPusher is a silent no-op (the common case in every test and
// any deployment with no trackers bound — see WithProgressPusher). Any push
// failure is logged at WARN, never surfaced — reading-triggered tracker
// sync is best-effort by design (spec §2: "reading-triggered failures are
// LOGGED + SWALLOWED", the same §16-sanctioned exception the reading-
// progress write itself already relies on).
func (s *Service) firePushProgress(ctx context.Context, seriesID uuid.UUID, chapterNumber float64) {
	if s.progressPusher == nil {
		return
	}
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := s.progressPusher.PushProgress(detached, seriesID, chapterNumber); err != nil {
			slog.WarnContext(detached, "series: tracker progress push failed", "series_id", seriesID, "err", err)
		}
	}()
}
