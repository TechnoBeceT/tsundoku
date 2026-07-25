package impersonate

import (
	"strconv"

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
}

// buildUpdates maps req's non-nil fields onto the settings.KeyValue batch
// SetMany expects. It only rejects the SHAPE (an empty body); the URL's
// blank-or-absolute-http(s) validation is enforced by settings.Service.SetMany
// itself (ErrInvalidSetting → 400 via mapServiceError), so this layer never
// duplicates that validation.
func buildUpdates(req UpdateRequest) ([]settingssvc.KeyValue, error) {
	var updates []settingssvc.KeyValue
	if req.Enabled != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyImpersonateEnabled, Value: strconv.FormatBool(*req.Enabled)})
	}
	if req.URL != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyImpersonateURL, Value: *req.URL})
	}
	if len(updates) == 0 {
		return nil, httperr.BadRequest("at least one setting must be provided")
	}
	return updates, nil
}
