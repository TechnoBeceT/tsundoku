package flaresolverr

import (
	"strconv"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	settingssvc "github.com/technobecet/tsundoku/internal/settings"
)

// UpdateRequest is the PATCH /api/flaresolverr/settings body. Every field is
// an optional pointer: a nil field is left untouched, so a partial body never
// clobbers an unset setting (mirrors the retired Suwayomi settings-proxy's
// FlareSolverrUpdate — same shape, same OpenAPI schema).
type UpdateRequest struct {
	Enabled            *bool   `json:"enabled"`
	URL                *string `json:"url"`
	Timeout            *int    `json:"timeout"`
	SessionName        *string `json:"sessionName"`
	SessionTTL         *int    `json:"sessionTtl"`
	AsResponseFallback *bool   `json:"asResponseFallback"`
}

// buildUpdates maps req's non-nil fields onto the settings.KeyValue batch
// SetMany expects. It only rejects the SHAPE (an empty body); per-field
// bounds (URL well-formedness, timeout/sessionTtl range) are enforced by
// settings.Service.SetMany itself (ErrInvalidSetting → 400 via
// mapServiceError), so this layer never duplicates that validation.
func buildUpdates(req UpdateRequest) ([]settingssvc.KeyValue, error) {
	var updates []settingssvc.KeyValue
	if req.Enabled != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyFlareSolverrEnabled, Value: strconv.FormatBool(*req.Enabled)})
	}
	if req.URL != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyFlareSolverrURL, Value: *req.URL})
	}
	if req.Timeout != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyFlareSolverrTimeout, Value: strconv.Itoa(*req.Timeout)})
	}
	if req.SessionName != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyFlareSolverrSessionName, Value: *req.SessionName})
	}
	if req.SessionTTL != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyFlareSolverrSessionTTL, Value: strconv.Itoa(*req.SessionTTL)})
	}
	if req.AsResponseFallback != nil {
		updates = append(updates, settingssvc.KeyValue{Key: settingssvc.KeyFlareSolverrResponseFallback, Value: strconv.FormatBool(*req.AsResponseFallback)})
	}
	if len(updates) == 0 {
		return nil, httperr.BadRequest("at least one setting must be provided")
	}
	return updates, nil
}
