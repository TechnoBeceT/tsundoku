package library

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/library"
)

// consolidateBody is the wire shape for POST
// /api/series/:id/providers/consolidate — the per-series multi-provider
// consolidation (QCAT-295 Part B). providerIds is the SET of provider ids to fold
// away; target names the ONE survivor to fold them into (see consolidateTarget).
type consolidateBody struct {
	ProviderIDs []string          `json:"providerIds"`
	Target      consolidateTarget `json:"target"`
}

// consolidateTarget is the discriminated-union target of a consolidation: EITHER
// an existing provider already on the series (ExistingProviderID) OR a
// match-to-real-source spec (Source). Exactly one must be set — validated by
// validateConsolidateBody.
type consolidateTarget struct {
	// ExistingProviderID folds the selected providers into this existing provider
	// (a UUID string). Mutually exclusive with Source.
	ExistingProviderID string `json:"existingProviderId,omitempty"`
	// Source, when set, attaches this engine-host source as the new survivor and
	// folds the selected providers into it (the match-to-source arm).
	Source *consolidateSource `json:"source,omitempty"`
}

// consolidateSource is the match-to-real-source arm of a consolidation target —
// the same {source,url,scanlator,importance} shape the single Match uses.
type consolidateSource struct {
	Source     string `json:"source"`
	URL        string `json:"url"`
	Scanlator  string `json:"scanlator"`
	Importance int    `json:"importance"`
}

// consolidateStartedResponse is the wire shape returned by POST
// /api/series/:id/providers/consolidate: {"started":true} on 202 once the async
// consolidation is launched, or {"started":false} on 409 when one is already in
// flight for this series.
type consolidateStartedResponse struct {
	Started bool `json:"started"`
}

// ConsolidateProviders handles POST /api/series/:id/providers/consolidate.
//
// It folds a SET of the series' providers into ONE survivor — either an existing
// provider on the series or a match-to-real-source target — WITHOUT
// re-downloading (see library.Service.ConsolidateProviders). Like the single
// Match (and for the same GAP-096 reason — the merge relabels many CBZs over NFS
// and runs for minutes), it runs the consolidation on a detached, time-bounded
// background goroutine (StartConsolidateProviders) and returns 202 immediately;
// the frontend refetches on the provider.merged SSE event the background op emits
// on completion. Returns 202 {started:true} once launched, or 409 {started:false}
// if a consolidation for this series is already in flight (single-flight guard).
// The request body is validated SYNCHRONOUSLY, so a malformed request still gets
// its 400 before anything is launched.
func (h *Handler) ConsolidateProviders(c echo.Context) error {
	id, err := validateID(c.Param("id"))
	if err != nil {
		return err
	}
	var body consolidateBody
	if err := c.Bind(&body); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid body")
	}
	mergeIDs, target, err := validateConsolidateBody(body)
	if err != nil {
		return err
	}

	started := h.svc.StartConsolidateProviders(c.Request().Context(), id, mergeIDs, target)
	if !started {
		return c.JSON(http.StatusConflict, consolidateStartedResponse{Started: false})
	}
	return c.JSON(http.StatusAccepted, consolidateStartedResponse{Started: true})
}

// validateConsolidateBody validates the consolidation request and builds the
// service inputs. Rules (fail-closed, 400 on any violation):
//   - providerIds is non-empty and every entry is a valid UUID (there must be at
//     least one provider to fold — combined with the target that is the ≥2
//     "providers involved" the feature requires).
//   - EXACTLY ONE target arm is set: existingProviderId XOR source.
//   - existing arm: existingProviderId is a valid UUID and is NOT also in the
//     merge set (the target can't fold into itself).
//   - source arm: source and url are non-blank and importance >= 1.
func validateConsolidateBody(body consolidateBody) ([]uuid.UUID, library.ConsolidateTarget, error) {
	mergeIDs, err := parseProviderIDs(body.ProviderIDs)
	if err != nil {
		return nil, library.ConsolidateTarget{}, err
	}

	hasExisting := body.Target.ExistingProviderID != ""
	hasSource := body.Target.Source != nil
	if hasExisting == hasSource {
		return nil, library.ConsolidateTarget{}, echo.NewHTTPError(http.StatusBadRequest, "exactly one target form is required: existingProviderId or source")
	}

	if hasExisting {
		existingID, perr := uuid.Parse(body.Target.ExistingProviderID)
		if perr != nil {
			return nil, library.ConsolidateTarget{}, echo.NewHTTPError(http.StatusBadRequest, "invalid target provider id")
		}
		for _, m := range mergeIDs {
			if m == existingID {
				return nil, library.ConsolidateTarget{}, echo.NewHTTPError(http.StatusBadRequest, "target provider must not be in the merge set")
			}
		}
		return mergeIDs, library.ConsolidateTarget{ExistingProviderID: &existingID}, nil
	}

	src := body.Target.Source
	if err := validateProviderRef(providerRefBody{Source: src.Source, URL: src.URL}); err != nil {
		return nil, library.ConsolidateTarget{}, err
	}
	if src.Importance < 1 {
		return nil, library.ConsolidateTarget{}, echo.NewHTTPError(http.StatusBadRequest, "importance must be >= 1")
	}
	return mergeIDs, library.ConsolidateTarget{
		Source:     src.Source,
		URL:        src.URL,
		Scanlator:  src.Scanlator,
		Importance: src.Importance,
	}, nil
}

// parseProviderIDs parses a non-empty list of provider-id strings into UUIDs,
// rejecting an empty list or any malformed id with a 400 (§3 validation at the
// boundary). Shared by the consolidate handler; kept here beside its only caller.
func parseProviderIDs(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "providerIds must not be empty")
	}
	ids := make([]uuid.UUID, len(raw))
	for i, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, echo.NewHTTPError(http.StatusBadRequest, "invalid provider id")
		}
		ids[i] = id
	}
	return ids, nil
}
