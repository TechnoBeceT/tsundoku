package account

import (
	"context"
	"log/slog"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
)

// MarkExpired flags trackerID's TrackerConnection row token_expired=true.
//
// BEST-EFFORT: a write failure here is logged, never returned — every
// caller already has (and is already surfacing) the real failure this flag
// merely annotates, whether that is ResolveToken's own tracker.ErrTokenExpired
// return (the PROACTIVE path) or a REACTIVE authed-call failure bind/syncsvc
// detect via errors.Is(err, tracker.ErrTokenExpired) after Search/GetEntry/
// SaveEntry/UpdateEntry. Losing this write must never mask or replace that
// original error.
//
// A bulk Where-scoped update (not an UpdateOne on an already-loaded row) so
// a REACTIVE caller — which only has a trackerID, never a loaded
// *ent.TrackerConnection — can call it the exact same way ResolveToken does.
func MarkExpired(ctx context.Context, client *ent.Client, trackerID int) {
	if _, err := client.TrackerConnection.Update().
		Where(enttrackerconnection.TrackerID(trackerID)).
		SetTokenExpired(true).
		Save(ctx); err != nil {
		slog.WarnContext(ctx, "account: failed to flag token expired", "tracker_id", trackerID, "err", err)
	}
}
