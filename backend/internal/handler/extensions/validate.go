package extensions

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
	"github.com/technobecet/tsundoku/internal/sourceengine"
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

// PreferenceUpdateRequest is the PATCH …/{pkgName}/preferences body: which
// source (sourceId) and which preference by KEY to write, plus the raw value.
// value is a json.RawMessage so it can be a bool, string, or string array — it
// is coerced to the correct Go type against the variant at that key (see
// coercePreferenceValue), because the same JSON type can back two variants
// (bool → checkbox OR switch; string → list OR editText).
type PreferenceUpdateRequest struct {
	// SourceID is the engine host source id the preference belongs to.
	SourceID string `json:"sourceId"`
	// Key is the source-internal preference key to write.
	Key string `json:"key"`
	// Value is the new value (bool / string / []string) — coerced by variant.
	Value json.RawMessage `json:"value"`
}

// validatePreferenceUpdate fail-closes the preference write body: sourceId must
// be non-blank and parse as a decimal int64, key must be non-blank, and value
// must be present. It returns the parsed sourceID and the trimmed key. The
// value's TYPE is validated later against the fetched variant
// (coercePreferenceValue) since it can't be judged without knowing the
// preference kind at that key.
func validatePreferenceUpdate(req PreferenceUpdateRequest) (int64, string, error) {
	rawID := strings.TrimSpace(req.SourceID)
	if rawID == "" {
		return 0, "", httperr.BadRequest("sourceId required")
	}
	sourceID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil {
		return 0, "", httperr.BadRequest("sourceId must be numeric")
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return 0, "", httperr.BadRequest("key required")
	}
	if len(req.Value) == 0 {
		return 0, "", httperr.BadRequest("value required")
	}
	return sourceID, key, nil
}

// SourceEnabledUpdateRequest is the PATCH /api/sources/:sourceId/enabled body.
// Enabled is a pointer so a missing key is rejected rather than silently
// defaulting to false (which would look like an owner-initiated disable).
type SourceEnabledUpdateRequest struct {
	// Enabled is the new per-language enable/disable state.
	Enabled *bool `json:"enabled"`
}

// validateSourceID trims the :sourceId path param, requires it non-blank, and
// parses it as the engine-host decimal int64 source id. A blank value or a
// non-numeric value is a 400.
func validateSourceID(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, httperr.BadRequest("sourceId required")
	}
	id, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, httperr.BadRequest("sourceId must be numeric")
	}
	return id, nil
}

// validateSourceEnabledUpdate fail-closes the enable/disable write body: enabled
// must be present (non-nil). It returns the requested state.
func validateSourceEnabledUpdate(req SourceEnabledUpdateRequest) (bool, error) {
	if req.Enabled == nil {
		return false, httperr.BadRequest("enabled required")
	}
	return *req.Enabled, nil
}

// coercePreferenceValue decodes the raw request value into the correctly-typed
// Go value for the variant at the target key: a bool for a checkbox/switch, a
// string for a list/edittext, a string array for a multi-select — the
// JSON-native shapes sourceengine.Client.SetPreferences' changes map expects.
// A value whose JSON type does not match the variant is a 400 (so the caller
// learns the mismatch as a clean validation error, not a raw engine-host 502).
//
// An explicit JSON null is rejected the same way as an absent value: `null`
// unmarshals cleanly into a zero bool/string/slice for every variant, so
// without this guard a null request would silently clear the preference
// instead of failing closed (mirrors the retired Suwayomi proxy's M3-1 fix).
func coercePreferenceValue(prefType string, raw json.RawMessage) (any, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, httperr.BadRequest("value required")
	}
	switch prefType {
	case sourceengine.PreferenceCheckBox, sourceengine.PreferenceSwitchCompat:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, httperr.BadRequest("value must be a boolean for this preference")
		}
		return b, nil
	case sourceengine.PreferenceList, sourceengine.PreferenceEditText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, httperr.BadRequest("value must be a string for this preference")
		}
		return s, nil
	case sourceengine.PreferenceMultiSelect:
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, httperr.BadRequest("value must be an array of strings for this preference")
		}
		return list, nil
	default:
		return nil, httperr.BadRequest("unknown preference type at this key")
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
