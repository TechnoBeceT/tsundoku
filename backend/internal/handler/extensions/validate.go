package extensions

import (
	"encoding/json"
	"strings"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// ReposUpdateRequest is the PUT /api/suwayomi/extensions/repos body. Repos is a
// plain slice so that BOTH a missing key and an explicit null unmarshal to nil
// (rejected as "must be a JSON array"), while an explicit empty array [] stays
// non-nil and is accepted (clear-all, replace semantics).
type ReposUpdateRequest struct {
	// Repos is the full replacement repo URL list (PUT = replace, not merge).
	Repos []string `json:"repos"`
}

// validatePkgName trims and requires a non-empty pkgName path param. A blank
// value is a 400; the trimmed value is returned for use as the extension identity.
func validatePkgName(raw string) (string, error) {
	pkgName := strings.TrimSpace(raw)
	if pkgName == "" {
		return "", httperr.BadRequest("pkgName required")
	}
	return pkgName, nil
}

// PreferenceUpdateRequest is the PATCH …/{pkgName}/preferences body: which source
// (sourceId) and which preference by POSITION to write, plus the raw value.
// value is a json.RawMessage so it can be a bool, string, or string array — it is
// coerced to the correct Go type against the variant at that position (see
// coercePreferenceValue), because the same JSON type can back two variants
// (bool → checkbox OR switch; string → list OR editText). position is a pointer so
// a missing key is rejected rather than silently defaulting to position 0.
type PreferenceUpdateRequest struct {
	// SourceID is the Suwayomi source id the preference belongs to.
	SourceID string `json:"sourceId"`
	// Position is the 0-based array index of the preference to write.
	Position *int `json:"position"`
	// Value is the new value (bool / string / []string) — coerced by variant.
	Value json.RawMessage `json:"value"`
}

// validatePreferenceUpdate fail-closes the preference write body: sourceId must be
// non-blank, position must be present and >= 0, and value must be present. It
// returns the trimmed sourceId and the position. The value's TYPE is validated
// later against the fetched variant (coercePreferenceValue) since it can't be
// judged without knowing the preference kind at that position.
func validatePreferenceUpdate(req PreferenceUpdateRequest) (string, int, error) {
	sourceID := strings.TrimSpace(req.SourceID)
	if sourceID == "" {
		return "", 0, httperr.BadRequest("sourceId required")
	}
	if req.Position == nil {
		return "", 0, httperr.BadRequest("position required")
	}
	if *req.Position < 0 {
		return "", 0, httperr.BadRequest("position must be >= 0")
	}
	if len(req.Value) == 0 {
		return "", 0, httperr.BadRequest("value required")
	}
	return sourceID, *req.Position, nil
}

// coercePreferenceValue decodes the raw request value into the correct typed
// PreferenceValue for the variant at the target position: a bool for a checkbox/
// switch, a string for a list/edittext, a string array for a multi-select. A
// value whose JSON type does not match the variant is a 400 (so the caller learns
// the mismatch as a clean validation error, not a raw Suwayomi 502).
func coercePreferenceValue(prefType suwayomicli.PreferenceType, raw json.RawMessage) (suwayomicli.PreferenceValue, error) {
	switch prefType {
	case suwayomicli.PreferenceCheckBox, suwayomicli.PreferenceSwitch:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return suwayomicli.PreferenceValue{}, httperr.BadRequest("value must be a boolean for this preference")
		}
		return suwayomicli.BoolPreferenceValue(prefType, b), nil
	case suwayomicli.PreferenceList, suwayomicli.PreferenceEditText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return suwayomicli.PreferenceValue{}, httperr.BadRequest("value must be a string for this preference")
		}
		return suwayomicli.StringPreferenceValue(prefType, s), nil
	case suwayomicli.PreferenceMultiSelect:
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return suwayomicli.PreferenceValue{}, httperr.BadRequest("value must be an array of strings for this preference")
		}
		return suwayomicli.MultiSelectPreferenceValue(list), nil
	default:
		return suwayomicli.PreferenceValue{}, httperr.BadRequest("unknown preference type at this position")
	}
}

// validateRepos fail-closes the repos replacement body:
//   - Repos must be present (a non-nil JSON array); null/missing → 400;
//   - an empty array is ALLOWED (clears all repos — the owner's explicit call);
//   - each entry is trimmed, must be non-blank, and must be an absolute http(s)
//     URL with a host (the same urlx.IsAbsoluteHTTP rule the FlareSolverr-URL
//     validator uses).
//
// It returns the trimmed list on success.
func validateRepos(req ReposUpdateRequest) ([]string, error) {
	if req.Repos == nil {
		return nil, httperr.BadRequest("repos must be a JSON array")
	}
	out := make([]string, 0, len(req.Repos))
	for _, raw := range req.Repos {
		repo := strings.TrimSpace(raw)
		if repo == "" {
			return nil, httperr.BadRequest("repos must not contain a blank URL")
		}
		if !urlx.IsAbsoluteHTTP(repo) {
			return nil, httperr.BadRequest("repos must contain only absolute http(s) URLs")
		}
		out = append(out, repo)
	}
	return out, nil
}
