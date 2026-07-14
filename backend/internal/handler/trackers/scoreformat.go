package trackers

import (
	"context"

	"github.com/technobecet/tsundoku/internal/ent"
	enttrackerconnection "github.com/technobecet/tsundoku/internal/ent/trackerconnection"
	"github.com/technobecet/tsundoku/internal/tracker"
	kernel "github.com/technobecet/tsundoku/internal/tracker/sync"
)

// defaultScoreFormat returns the scale a binding for trackerID should be
// interpreted on when its TrackerConnection has never captured a
// score_format — currently every tracker except AniList (see
// tracker.AccountInfoProvider's doc comment: only AniList implements the
// self-lookup that populates TrackerConnection.score_format at login).
// AniList's own default for a fresh account is POINT_100, so that is this
// tracker's fallback too; MAL's score field is a fixed 0-10 scale; Kitsu's
// wire field (ratingTwenty) is a fixed 0-20 scale; MangaUpdates has no
// native user-score field at all. An unregistered/unknown trackerID returns
// "" — there is no native scale to report.
func defaultScoreFormat(trackerID int) string {
	switch trackerID {
	case tracker.IDAniList:
		return string(kernel.ScoreFormatAniListPoint100)
	case tracker.IDMAL:
		return string(kernel.ScoreFormatMAL)
	case tracker.IDKitsu:
		return string(kernel.ScoreFormatKitsuRatingTwenty)
	case tracker.IDMangaUpdates:
		return string(kernel.ScoreFormatMangaUpdates)
	default:
		return ""
	}
}

// resolveScoreFormat returns trackerID's EFFECTIVE score format for
// TrackBindingDTO.ScoreFormat: the connected account's own captured
// score_format (TrackerConnection.score_format, set at login for a tracker
// that exposes one — currently AniList only) when non-empty, else
// defaultScoreFormat(trackerID) — so a binding for a tracker with no
// connection row (never logged in, or logged out since) or a connection
// that never captured one still tells the client which native scale its
// Score is stored on. Used by the single-binding endpoints
// (CreateBinding/RefreshBinding/UpdateTrack), where one query per request is
// not an N+1 concern.
func (h *Handler) resolveScoreFormat(ctx context.Context, trackerID int) (string, error) {
	conn, err := h.client.TrackerConnection.Query().
		Where(enttrackerconnection.TrackerID(trackerID)).
		Only(ctx)
	switch {
	case ent.IsNotFound(err):
		return defaultScoreFormat(trackerID), nil
	case err != nil:
		return "", err
	case conn.ScoreFormat != "":
		return conn.ScoreFormat, nil
	default:
		return defaultScoreFormat(trackerID), nil
	}
}

// resolveScoreFormats batch-resolves resolveScoreFormat's result for every
// DISTINCT tracker_id present in bindings — ONE query total (loads every
// TrackerConnection row, mirroring Handler.List's own byTrackerID batch
// pattern; the registry is at most a handful of trackers, so an unfiltered
// load costs nothing) regardless of how many bindings are being rendered.
// Used by the list-returning endpoints (ListBindings/SyncTracking) so
// mapping N bindings never issues N queries.
func (h *Handler) resolveScoreFormats(ctx context.Context, bindings []*ent.TrackBinding) (map[int]string, error) {
	rows, err := h.client.TrackerConnection.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	byTrackerID := make(map[int]string, len(rows))
	for _, r := range rows {
		byTrackerID[r.TrackerID] = r.ScoreFormat
	}

	out := make(map[int]string, len(bindings))
	for _, b := range bindings {
		if _, ok := out[b.TrackerID]; ok {
			continue
		}
		if sf := byTrackerID[b.TrackerID]; sf != "" {
			out[b.TrackerID] = sf
		} else {
			out[b.TrackerID] = defaultScoreFormat(b.TrackerID)
		}
	}
	return out, nil
}
