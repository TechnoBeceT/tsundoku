package metadata

import (
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// IdentifyRequest is the POST /api/series/:id/metadata/identify body — the
// owner's picked candidate (from the Search gallery) that becomes the
// series' primary metadata_source.
type IdentifyRequest struct {
	// Provider is the metadata Provider's Key() (e.g. "anilist").
	Provider string `json:"provider"`
	// RemoteID is the provider's own identifier for the picked series.
	RemoteID string `json:"remoteId"`
}

// SetCoverRequest is the POST /api/series/:id/cover body — the owner's
// explicit cover pick.
type SetCoverRequest struct {
	// SourceKind is "metadata" or "source" (see metadata.CoverCandidate).
	SourceKind string `json:"sourceKind"`
	// SourceRef is the metadata Provider's Key() when SourceKind is
	// "metadata", or the SeriesProvider UUID string when SourceKind is
	// "source".
	SourceRef string `json:"sourceRef"`
	// CoverURL is the absolute http(s) URL the cover bytes are fetched from.
	CoverURL string `json:"coverUrl"`
}

// validateID parses a UUID path param. subject names which id is being parsed
// ("series id") so a malformed value yields a precise 400 body
// ("invalid <subject>"). The central middleware renders the message as
// {"message":...}.
func validateID(raw, subject string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, httperr.BadRequest("invalid " + subject)
	}
	return id, nil
}

// validateQuery validates the required ?q search query parameter. An empty or
// absent value yields a 400.
func validateQuery(raw string) (string, error) {
	q := strings.TrimSpace(raw)
	if q == "" {
		return "", httperr.BadRequest("q is required and must be non-empty")
	}
	return q, nil
}

// validateIdentify requires both provider and remoteId to be non-blank —
// there is no meaningful "identify against nothing" request. Values are
// trimmed before use.
func validateIdentify(req IdentifyRequest) (provider, remoteID string, err error) {
	provider = strings.TrimSpace(req.Provider)
	remoteID = strings.TrimSpace(req.RemoteID)
	if provider == "" {
		return "", "", httperr.BadRequest("provider is required")
	}
	if remoteID == "" {
		return "", "", httperr.BadRequest("remoteId is required")
	}
	return provider, remoteID, nil
}

// validateSetCover requires sourceKind and sourceRef to be non-blank and
// coverUrl to be a well-formed absolute http(s) URL (the shared
// urlx.IsAbsoluteHTTP kernel — reused by the FlareSolverr-URL and
// extension-repo-URL validators so "valid absolute http(s) URL" is defined in
// exactly one place). Unlike the FlareSolverr validator, an empty coverUrl is
// NOT allowed here — SetCover has no "clear the cover" meaning, only "set it
// to this URL".
func validateSetCover(req SetCoverRequest) error {
	if strings.TrimSpace(req.SourceKind) == "" {
		return httperr.BadRequest("sourceKind is required")
	}
	if strings.TrimSpace(req.SourceRef) == "" {
		return httperr.BadRequest("sourceRef is required")
	}
	if !urlx.IsAbsoluteHTTP(req.CoverURL) {
		return httperr.BadRequest("coverUrl must be a valid absolute http(s) URL")
	}
	return nil
}
