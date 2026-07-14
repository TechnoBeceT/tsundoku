package trackers

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/tracker/syncsvc"
)

// validateTrackerID parses the :id path param used by every /api/trackers/:id
// route — the tracker's numeric registry id (tracker.ID* constants), NOT a
// UUID. A non-positive or non-numeric value is a 400.
func validateTrackerID(raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, httperr.BadRequest("invalid tracker id")
	}
	return id, nil
}

// validateUUID parses a UUID path param. subject names which id is being
// parsed ("series id", "record id") so a malformed value yields a precise
// 400 body ("invalid <subject>").
func validateUUID(raw, subject string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httperr.BadRequest("invalid " + subject)
	}
	return id, nil
}

// validateQuery validates the required ?q search query parameter for
// GET /api/trackers/:id/search. An empty or absent value yields a 400.
func validateQuery(raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", httperr.BadRequest("q is required and must be non-empty")
	}
	return q, nil
}

// OAuthLoginRequest is the POST /api/trackers/:id/login/oauth body.
// callbackUrl is the FULL callback URL the SPA's own OAuth callback route
// received — carrying either "code" (MAL) or "access_token" (AniList,
// delivered in the URL FRAGMENT). The SPA forwards window.location.href
// verbatim, fragment intact — it does NOT pre-convert the fragment into a
// query param — since connect.Service.callbackParams reads both. No "state"
// param is involved: correlation with the pending login is by the :id path
// param alone (see internal/tracker/connect's package doc comment).
type OAuthLoginRequest struct {
	CallbackURL string `json:"callbackUrl"`
}

// validateOAuthLogin requires a non-blank callbackUrl.
func validateOAuthLogin(req OAuthLoginRequest) (string, error) {
	cb := strings.TrimSpace(req.CallbackURL)
	if cb == "" {
		return "", httperr.BadRequest("callbackUrl is required")
	}
	return cb, nil
}

// CredentialLoginRequest is the POST /api/trackers/:id/login/credentials
// body — a direct username/password login for a credential-based tracker
// (Kitsu, MangaUpdates).
type CredentialLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// validateCredentialLogin requires both username and password to be
// non-blank. The password is returned verbatim (never trimmed — a password
// legitimately may carry leading/trailing whitespace) and is never logged by
// any caller of this function.
func validateCredentialLogin(req CredentialLoginRequest) (username, password string, err error) {
	username = strings.TrimSpace(req.Username)
	if username == "" {
		return "", "", httperr.BadRequest("username is required")
	}
	if req.Password == "" {
		return "", "", httperr.BadRequest("password is required")
	}
	return username, req.Password, nil
}

// BindRequest is the POST /api/series/:id/tracking body — the owner's picked
// tracker + remote entry (from the search results) to bind the series to.
type BindRequest struct {
	TrackerID int    `json:"trackerId"`
	RemoteID  string `json:"remoteId"`
}

// validateBind requires a positive trackerId and a non-blank remoteId.
func validateBind(req BindRequest) (trackerID int, remoteID string, err error) {
	if req.TrackerID <= 0 {
		return 0, "", httperr.BadRequest("trackerId is required")
	}
	remoteID = strings.TrimSpace(req.RemoteID)
	if remoteID == "" {
		return 0, "", httperr.BadRequest("remoteId is required")
	}
	return req.TrackerID, remoteID, nil
}

// validateDeleteRemote parses the optional ?deleteRemote query param for
// DELETE /api/series/:id/tracking/:recordId. Unlike the whole-series delete's
// REQUIRED deleteFiles param, this one defaults to false when absent — an
// ordinary unbind (never touching the remote account) is the common case.
// An explicitly present but non-boolean value is still a 400 (fail closed on
// a malformed value rather than silently defaulting it).
func validateDeleteRemote(raw string) (bool, error) {
	switch raw {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	default:
		return false, httperr.BadRequest("deleteRemote must be true or false")
	}
}

// UpdateTrackRequest is the POST /api/series/:id/tracking/:recordId/update
// body — the owner's manual tracking-sheet edit. Every field is an optional
// pointer (mirrors suwayomi's partial UpdateRequest): a nil field means
// "leave unchanged", so the request is a genuine partial update. Date
// fields decode as RFC3339 timestamps (encoding/json's native *time.Time
// support) — no custom parsing needed.
type UpdateTrackRequest struct {
	Status          *string    `json:"status"`
	LastChapterRead *float64   `json:"lastChapterRead"`
	Score           *float64   `json:"score"`
	StartDate       *time.Time `json:"startDate"`
	FinishDate      *time.Time `json:"finishDate"`
	Private         *bool      `json:"private"`
}

// validateUpdateTrack fail-closes the request and maps it to a
// syncsvc.UpdatePatch. Rules:
//   - at least one field must be provided (an all-nil body → 400, mirrors
//     suwayomi's validateUpdate empty-patch guard);
//   - status, when set, must be non-blank (there is no "clear the status"
//     concept — a tracker entry always carries one);
//   - lastChapterRead / score, when set, must be non-negative.
//
// Per-field checks are split into updateTrackHasAnyField/
// validateUpdateTrackFields so this stays under the fleet's per-function
// cyclomatic-complexity budget.
func validateUpdateTrack(req UpdateTrackRequest) (syncsvc.UpdatePatch, error) {
	if !updateTrackHasAnyField(req) {
		return syncsvc.UpdatePatch{}, httperr.BadRequest("at least one field must be provided")
	}
	if err := validateUpdateTrackFields(req); err != nil {
		return syncsvc.UpdatePatch{}, err
	}
	return syncsvc.UpdatePatch{
		Status:          req.Status,
		LastChapterRead: req.LastChapterRead,
		Score:           req.Score,
		StartDate:       req.StartDate,
		FinishDate:      req.FinishDate,
		Private:         req.Private,
	}, nil
}

// updateTrackHasAnyField reports whether at least one patch field is set.
func updateTrackHasAnyField(req UpdateTrackRequest) bool {
	return req.Status != nil || req.LastChapterRead != nil || req.Score != nil ||
		req.StartDate != nil || req.FinishDate != nil || req.Private != nil
}

// validateUpdateTrackFields checks the constraints on the individual
// set fields (status non-blank; lastChapterRead/score non-negative).
func validateUpdateTrackFields(req UpdateTrackRequest) error {
	if req.Status != nil && strings.TrimSpace(*req.Status) == "" {
		return httperr.BadRequest("status must be non-empty when provided")
	}
	if req.LastChapterRead != nil && *req.LastChapterRead < 0 {
		return httperr.BadRequest("lastChapterRead must be >= 0")
	}
	if req.Score != nil && *req.Score < 0 {
		return httperr.BadRequest("score must be >= 0")
	}
	return nil
}
