// Package sourcethroughput contains the thin owner HTTP surface for global
// defaults and per-source throughput overrides.
package sourcethroughput

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	policy "github.com/technobecet/tsundoku/internal/sourcethroughput"
)

type patchMode string

const (
	modeInherit  patchMode = "inherit"
	modeOverride patchMode = "override"
)

type intPatchRequest struct {
	Mode  patchMode       `json:"mode"`
	Value json.RawMessage `json:"value"`
}

type durationPatchRequest struct {
	Mode  patchMode       `json:"mode"`
	Value json.RawMessage `json:"value"`
}

type updateRequest struct {
	DownloadConcurrency *intPatchRequest      `json:"downloadConcurrency"`
	ImageRequestDelay   *durationPatchRequest `json:"imageRequestDelay"`
}

func decodeUpdateRequest(body io.Reader) (updateRequest, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return updateRequest{}, echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return updateRequest{}, err
	}

	var request updateRequest
	for name, raw := range fields {
		if err := request.decodeField(name, raw); err != nil {
			return updateRequest{}, err
		}
	}
	return request, nil
}

func (r *updateRequest) decodeField(name string, raw json.RawMessage) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return invalidField(name, "must not be null")
	}
	switch name {
	case "downloadConcurrency":
		return decodeRequestField(raw, name, &r.DownloadConcurrency)
	case "imageRequestDelay":
		return decodeRequestField(raw, name, &r.ImageRequestDelay)
	default:
		return invalidField(name, "unknown field")
	}
}

func decodeRequestField[T any](raw json.RawMessage, name string, destination **T) error {
	var field T
	if err := decodeStrict(raw, &field); err != nil {
		return invalidField(name, "invalid object")
	}
	*destination = &field
	return nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return echo.NewHTTPError(http.StatusBadRequest, "request body must contain one JSON object")
	}
	return nil
}

func parseSourceID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	return id, nil
}

func (r updateRequest) toPatch() (policy.Patch, error) {
	if r.DownloadConcurrency == nil && r.ImageRequestDelay == nil {
		return policy.Patch{}, echo.NewHTTPError(http.StatusBadRequest, "at least one throughput field is required")
	}
	var patch policy.Patch
	if r.DownloadConcurrency != nil {
		field, err := r.DownloadConcurrency.toPatch()
		if err != nil {
			return policy.Patch{}, err
		}
		patch.DownloadConcurrency = field
	}
	if r.ImageRequestDelay != nil {
		field, err := r.ImageRequestDelay.toPatch()
		if err != nil {
			return policy.Patch{}, err
		}
		patch.ImageRequestDelay = field
	}
	return patch, nil
}

func (r intPatchRequest) toPatch() (policy.PatchField[int], error) {
	switch r.Mode {
	case modeInherit:
		if len(r.Value) != 0 {
			return policy.PatchField[int]{}, invalidField("downloadConcurrency", "inherit forbids value")
		}
		return policy.Clear[int](), nil
	case modeOverride:
		if len(r.Value) == 0 || bytes.Equal(bytes.TrimSpace(r.Value), []byte("null")) {
			return policy.PatchField[int]{}, invalidField("downloadConcurrency", "override requires value")
		}
		var value int
		if err := json.Unmarshal(r.Value, &value); err != nil {
			return policy.PatchField[int]{}, invalidField("downloadConcurrency", "value must be an integer")
		}
		field := policy.Set(value)
		if err := validateServicePatch(policy.Patch{DownloadConcurrency: field}); err != nil {
			return policy.PatchField[int]{}, err
		}
		return field, nil
	default:
		return policy.PatchField[int]{}, invalidField("downloadConcurrency", "mode must be inherit or override")
	}
}

func (r durationPatchRequest) toPatch() (policy.PatchField[time.Duration], error) {
	switch r.Mode {
	case modeInherit:
		if len(r.Value) != 0 {
			return policy.PatchField[time.Duration]{}, invalidField("imageRequestDelay", "inherit forbids value")
		}
		return policy.Clear[time.Duration](), nil
	case modeOverride:
		if len(r.Value) == 0 || bytes.Equal(bytes.TrimSpace(r.Value), []byte("null")) {
			return policy.PatchField[time.Duration]{}, invalidField("imageRequestDelay", "override requires value")
		}
		var raw string
		if err := json.Unmarshal(r.Value, &raw); err != nil {
			return policy.PatchField[time.Duration]{}, invalidField("imageRequestDelay", "value must be a duration")
		}
		value, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil {
			return policy.PatchField[time.Duration]{}, invalidField("imageRequestDelay", "value must be a duration")
		}
		field := policy.Set(value)
		if err := validateServicePatch(policy.Patch{ImageRequestDelay: field}); err != nil {
			return policy.PatchField[time.Duration]{}, err
		}
		return field, nil
	default:
		return policy.PatchField[time.Duration]{}, invalidField("imageRequestDelay", "mode must be inherit or override")
	}
}

func validateServicePatch(patch policy.Patch) error {
	// The service is the authoritative range validator. A temporary zero-value
	// field represents keep, so validating one field never changes the other.
	if err := policy.Validate(patch); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return nil
}

func invalidField(field, reason string) error {
	return echo.NewHTTPError(http.StatusBadRequest, field+": "+reason)
}

func mapServiceError(err error) error {
	if errors.Is(err, policy.ErrInvalidPolicy) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return err
}
