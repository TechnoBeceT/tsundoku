// Package syncsvc is the Phase-4c tracker SYNC service: it applies the pure
// rule kernel (internal/tracker/sync) against real TrackBinding rows —
// pushing local reading progress to bound trackers (PushProgress), pulling +
// converging remote progress (SyncNow), and applying the owner's manual
// tracking-sheet edits (UpdateTrack) — and implements the durable retry
// queue's Pusher seam (internal/tracker/retry.Pusher) so a failed push is
// never lost. See spec/trackers-sync-phase4 §2/§4.
//
// This package (like internal/tracker/bind and internal/tracker/connect
// before it) DOES use ent — it is the ent-touching orchestration layer that
// sits ABOVE the ent-free internal/tracker port + the ent-free internal/
// tracker/sync rule kernel, never the reverse. It depends on the tracker
// Registry (which tracker to call) + the retry Queue (where a failed push
// goes) + a narrow SidecarSyncer port (reused from internal/tracker/bind, so
// the TrackBinding↔sidecar mirror logic lives in exactly ONE place, §2 DRY).
package syncsvc

import (
	"context"
	"errors"

	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/tracker"
	"github.com/technobecet/tsundoku/internal/tracker/account"
	"github.com/technobecet/tsundoku/internal/tracker/retry"

	"github.com/google/uuid"
)

// Sentinel errors. The HTTP handler layer maps these to their documented
// status codes, mirroring internal/tracker/bind's own sentinel shape.
var (
	// ErrTrackerNotFound is returned when a binding's tracker_id does not
	// match any tracker in the Service's Registry.
	ErrTrackerNotFound = errors.New("syncsvc: unknown tracker id")
	// ErrTrackerNotConnected is returned when a binding's tracker has no
	// TrackerConnection row (the owner has never logged in, or logged out).
	ErrTrackerNotConnected = errors.New("syncsvc: tracker is not connected")
	// ErrBindingNotFound is returned by UpdateTrack when recordID matches no
	// TrackBinding row.
	ErrBindingNotFound = errors.New("syncsvc: binding not found")
)

// SidecarSyncer mirrors a series' current TrackBinding set into its
// tsundoku.json sidecar — satisfied by bind.Service.SyncSidecar. Depending
// on this narrow port rather than importing internal/tracker/bind's whole
// concrete type keeps this package trivially fakeable in tests and keeps
// the sidecar-durability write in its ONE existing home (bind.Service
// already owns it for Bind/Unbind/FetchTrack) instead of a second
// read-all-bindings-then-write-sidecar implementation here (§2 DRY).
type SidecarSyncer interface {
	SyncSidecar(ctx context.Context, seriesID uuid.UUID)
}

// AutoUpdateTracker reports whether the reading-triggered tracker-sync push
// is currently enabled — satisfied by settings.Service.AutoUpdateTrack /
// settings.Static (the settings.jobs.auto_update_track tunable, default
// true). PushProgress consults it so the gate lives in ONE place regardless
// of which caller (the reader hook, a future bulk trigger) fires a push; a
// nil AutoUpdateTracker is treated as "always enabled" (the common test
// shape — most tests don't care about the toggle).
type AutoUpdateTracker interface {
	AutoUpdateTrack(ctx context.Context) bool
}

// Service is the tracker sync service.
type Service struct {
	client     *ent.Client
	registry   *tracker.Registry
	retryQueue *retry.Queue
	sidecar    SidecarSyncer
	autoUpdate AutoUpdateTracker
}

// NewService builds a Service. retryQueue is where a failed push is
// durably enqueued (internal/tracker/retry — never-lose-progress); sidecar
// mirrors a series' bindings to disk after every mutation (bind.Service
// already implements SidecarSyncer); autoUpdate gates PushProgress on the
// auto_update_track setting (nil = always enabled, e.g. in tests that don't
// exercise the toggle).
func NewService(client *ent.Client, registry *tracker.Registry, retryQueue *retry.Queue, sidecar SidecarSyncer, autoUpdate AutoUpdateTracker) *Service {
	return &Service{
		client:     client,
		registry:   registry,
		retryQueue: retryQueue,
		sidecar:    sidecar,
		autoUpdate: autoUpdate,
	}
}

// accountToken loads trackerID's connected account's current, USABLE
// access token via the shared internal/tracker/account resolver — see
// account.ResolveToken's own doc comment for the proactive-refresh +
// token_expired-flagging behavior this closes (pre-activation gap: a
// stored token used to be returned verbatim, never refreshed, until it
// 401'd forever with the UI still showing "connected"). Mirrors
// bind.Service.accountToken's same shape (both now delegate to the ONE
// shared resolver instead of duplicating a raw "return conn.AccessToken"
// read); account.ErrTrackerNotConnected is translated to THIS package's own
// sentinel of the same shape so mapServiceError's existing errors.Is checks
// keep matching unchanged.
func (s *Service) accountToken(ctx context.Context, trackerID int) (string, error) {
	token, err := account.ResolveToken(ctx, s.client, s.registry, trackerID)
	if err != nil {
		if errors.Is(err, account.ErrTrackerNotConnected) {
			return "", ErrTrackerNotConnected
		}
		return "", err
	}
	return token, nil
}

// markExpiredOnTokenFailure flags trackerID's connection token_expired when
// err is tracker.ErrTokenExpired — the REACTIVE signal (an authed
// GetEntry/UpdateEntry call itself came back reporting the token dead),
// distinct from accountToken's PROACTIVE pre-call check. Best-effort via
// account.MarkExpired; returns nothing, so it can never mask or replace the
// original err at any call site.
func (s *Service) markExpiredOnTokenFailure(ctx context.Context, trackerID int, err error) {
	if errors.Is(err, tracker.ErrTokenExpired) {
		account.MarkExpired(ctx, s.client, trackerID)
	}
}

// syncSidecar calls the injected SidecarSyncer when one is attached — a nil
// sidecar (an uncommon test shape) is a silent no-op, mirroring every other
// optional-dependency port in this codebase (series.CoverFetcher, imports.
// AutoIdentifier).
func (s *Service) syncSidecar(ctx context.Context, seriesID uuid.UUID) {
	if s.sidecar == nil {
		return
	}
	s.sidecar.SyncSidecar(ctx, seriesID)
}
