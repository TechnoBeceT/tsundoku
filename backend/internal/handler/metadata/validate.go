package metadata

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/metadatasvc"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// IdentifyRequest is the POST /api/series/:id/metadata/identify body. It
// accepts EITHER shape:
//   - the legacy single-pick {provider, remoteId} (back-compat — routes to
//     metadatasvc.Service.Identify's own anchor-then-aggregate behavior);
//   - the multi-select {selections:[...]} (routes to
//     metadatasvc.Service.IdentifyMerge when it carries more than one entry).
//
// When Selections is non-empty it wins outright — the legacy fields are
// ignored (validateIdentify never mixes the two shapes).
type IdentifyRequest struct {
	// Provider is the metadata Provider's Key() (e.g. "anilist"). Legacy
	// single-pick shape; ignored when Selections is non-empty.
	Provider string `json:"provider"`
	// RemoteID is the provider's own identifier for the picked series.
	// Legacy single-pick shape; ignored when Selections is non-empty.
	RemoteID string `json:"remoteId"`
	// Selections is the multi-select merge picks, in priority order —
	// Selections[0] is primary (anchors scalar precedence + metadataSource).
	Selections []SelectionRequest `json:"selections"`
}

// SelectionRequest is one entry of IdentifyRequest.Selections — mirrors the
// legacy {provider, remoteId} pair shape so the two request forms share a
// field vocabulary.
type SelectionRequest struct {
	Provider string `json:"provider"`
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

// validateIdentify normalizes IdentifyRequest into the selections
// metadatasvc consumes. A non-empty Selections wins outright (the multi-
// select shape); otherwise it falls back to the legacy {provider, remoteId}
// pair, wrapped as a single selection. Either way, every provider/remoteId
// value is trimmed and required non-blank — there is no meaningful "identify
// against nothing" request.
func validateIdentify(req IdentifyRequest) ([]metadatasvc.Selection, error) {
	if len(req.Selections) > 0 {
		out := make([]metadatasvc.Selection, 0, len(req.Selections))
		for i, sel := range req.Selections {
			provider := strings.TrimSpace(sel.Provider)
			remoteID := strings.TrimSpace(sel.RemoteID)
			if provider == "" {
				return nil, httperr.BadRequest(fmt.Sprintf("selections[%d].provider is required", i))
			}
			if remoteID == "" {
				return nil, httperr.BadRequest(fmt.Sprintf("selections[%d].remoteId is required", i))
			}
			out = append(out, metadatasvc.Selection{Provider: provider, RemoteID: remoteID})
		}
		return out, nil
	}

	provider := strings.TrimSpace(req.Provider)
	remoteID := strings.TrimSpace(req.RemoteID)
	if provider == "" {
		return nil, httperr.BadRequest("provider is required")
	}
	if remoteID == "" {
		return nil, httperr.BadRequest("remoteId is required")
	}
	return []metadatasvc.Selection{{Provider: provider, RemoteID: remoteID}}, nil
}

// validSourceKinds is the closed allowlist SetCoverRequest.SourceKind must
// belong to (mirrors metadata.CoverCandidate's two producers: a metadata
// provider hit, or a library SeriesProvider). SourceKind ultimately flows
// into SourceRef's disk.SaveCover-and-persisted Provider tag, so an
// unvalidated free-form string here is an under-validated sink argument, not
// just a display label.
var validSourceKinds = map[string]bool{
	"metadata": true,
	"source":   true,
}

// validateSetCover requires sourceKind to be one of validSourceKinds and
// sourceRef to be non-blank. coverUrl's shape depends on sourceKind:
//   - "metadata": a well-formed absolute http(s) URL (the shared
//     urlx.IsAbsoluteHTTP kernel — reused by the FlareSolverr-URL and
//     extension-repo-URL validators so "valid absolute http(s) URL" is
//     defined in exactly one place) — metadatasvc.SetCover fetches it by a
//     plain HTTP GET, so it must genuinely be reachable that way.
//   - "source": the same-origin cover PROXY path CoverCandidates handed back
//     (/api/series/{id}/providers/{providerId}/cover — see
//     metadatasvc.sourceCoverCandidates) is NOT an absolute URL (no scheme,
//     no host), so it would always fail IsAbsoluteHTTP; SetCover never
//     fetches it directly for this kind (real bytes are resolved through the
//     SourceCoverFetcher port instead) — only non-blank is required.
//
// Unlike the FlareSolverr validator, an empty coverUrl is NOT allowed for
// either kind — SetCover has no "clear the cover" meaning, only "set it to
// this URL".
func validateSetCover(req SetCoverRequest) error {
	if !validSourceKinds[req.SourceKind] {
		return httperr.BadRequest("invalid sourceKind")
	}
	if strings.TrimSpace(req.SourceRef) == "" {
		return httperr.BadRequest("sourceRef is required")
	}
	if req.SourceKind != "metadata" {
		if strings.TrimSpace(req.CoverURL) == "" {
			return httperr.BadRequest("coverUrl is required")
		}
		return nil
	}
	if !urlx.IsAbsoluteHTTP(req.CoverURL) {
		return httperr.BadRequest("coverUrl must be a valid absolute http(s) URL")
	}
	return nil
}
