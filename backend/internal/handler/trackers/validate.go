package trackers

import (
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
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

// OAuthLoginRequest is the POST /api/trackers/:id/login/oauth body. callbackUrl
// is the FULL callback URL the SPA's own OAuth callback route received —
// carrying "state" plus either "code" (MAL) or "access_token" (AniList; the
// SPA turns the browser's URL fragment into a query param before posting
// here, since a server never sees a fragment).
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
