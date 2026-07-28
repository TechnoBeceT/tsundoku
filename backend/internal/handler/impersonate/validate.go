package impersonate

import (
	"strconv"
	"strings"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// UpdateRequest is the PUT /api/impersonate body. Every field is an optional
// pointer: a nil field is left untouched, so a partial body never clobbers an
// unset setting (mirrors handler/flaresolverr's UpdateRequest — same partial
// shape).
type UpdateRequest struct {
	Enabled *bool   `json:"enabled"`
	URL     *string `json:"url"`
	// SourceIDs is the per-source gating set (GAP-131) as stringified 64-bit
	// source ids. A nil pointer leaves the stored selection untouched; an
	// explicitly EMPTY array clears it (no source uses the gateway) — the two are
	// deliberately distinguishable, which is why this is a pointer-to-slice.
	SourceIDs *[]string `json:"sourceIds"`
}

// buildUpdates maps req's non-nil fields onto the settings.KeyValue batch
// SetMany expects. It only rejects the SHAPE (an empty body); the VALUE
// validation — the URL's blank-or-absolute-http(s) rule and the source set's
// "every entry is a non-negative decimal id" rule — is enforced by
// settings.Service.SetMany itself (ErrInvalidSetting → 400 via mapServiceError),
// so this layer never duplicates it.
func buildUpdates(req UpdateRequest) ([]settingssvc.KeyValue, error) {
	var updates []settingssvc.KeyValue
	if req.Enabled != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyImpersonateEnabled, Value: strconv.FormatBool(*req.Enabled)})
	}
	if req.URL != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyImpersonateURL, Value: *req.URL})
	}
	if req.SourceIDs != nil {
		// The overlay stores the set as one comma-separated value; joining here
		// keeps the id-format rule in the single place that owns it (the tunable's
		// validator), so a bad entry surfaces as the same ErrInvalidSetting → 400
		// any other malformed setting does.
		updates = append(updates, settingssvc.KeyValue{
			Key:   settingssvc.KeyImpersonateSources,
			Value: strings.Join(*req.SourceIDs, ","),
		})
	}
	if len(updates) == 0 {
		return nil, httperr.BadRequest("at least one setting must be provided")
	}
	return updates, nil
}
