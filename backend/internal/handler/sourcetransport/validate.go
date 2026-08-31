package sourcetransport

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/technobecet/tsundoku/internal/sourcetransport"
)

func parseSourceID(raw string) (int64, error) {
	if !isSignedDecimal(raw) {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	return id, nil
}

func isSignedDecimal(raw string) bool {
	if raw == "" {
		return false
	}
	start := 0
	if raw[0] == '-' {
		if len(raw) == 1 {
			return false
		}
		start = 1
	}
	for i := start; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	return true
}

func decodePatch(body io.Reader) (sourcetransport.Patch, error) {
	fields, err := decodeObject(body)
	if err != nil {
		return sourcetransport.Patch{}, err
	}
	if len(fields) > 2 {
		return sourcetransport.Patch{}, invalidBody()
	}

	var patch sourcetransport.Patch
	for name, raw := range fields {
		switch name {
		case "reuseBypassSession":
			patch.ReuseBypassSession, err = decodeBooleanPatch(raw)
		case "imageConnectionMode":
			patch.ImageConnectionMode, err = decodeImageConnectionPatch(raw)
		default:
			return sourcetransport.Patch{}, invalidBody()
		}
		if err != nil {
			return sourcetransport.Patch{}, err
		}
	}
	return patch, nil
}

func decodeObject(body io.Reader) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(body)
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, invalidBody()
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, echo.NewHTTPError(http.StatusBadRequest, "request body must contain one JSON object")
	}
	return fields, nil
}

func decodeBooleanPatch(raw json.RawMessage) (sourcetransport.PatchField[bool], error) {
	fields, err := decodeRawObject(raw)
	if err != nil {
		return sourcetransport.PatchField[bool]{}, err
	}
	mode, err := patchMode(fields)
	if err != nil {
		return sourcetransport.PatchField[bool]{}, err
	}
	switch mode {
	case "inherit":
		return decodeInheritedPatch[bool](fields)
	case "override":
		return decodeOverridePatch[bool](fields, nil)
	default:
		return sourcetransport.PatchField[bool]{}, invalidBody()
	}
}

func decodeImageConnectionPatch(raw json.RawMessage) (sourcetransport.PatchField[sourcetransport.ImageConnectionMode], error) {
	fields, err := decodeRawObject(raw)
	if err != nil {
		return sourcetransport.PatchField[sourcetransport.ImageConnectionMode]{}, err
	}
	mode, err := patchMode(fields)
	if err != nil {
		return sourcetransport.PatchField[sourcetransport.ImageConnectionMode]{}, err
	}
	switch mode {
	case "inherit":
		return decodeInheritedPatch[sourcetransport.ImageConnectionMode](fields)
	case "override":
		return decodeOverridePatch(fields, func(value sourcetransport.ImageConnectionMode) bool {
			return value == sourcetransport.ImageConnectionFresh || value == sourcetransport.ImageConnectionReuse
		})
	default:
		return sourcetransport.PatchField[sourcetransport.ImageConnectionMode]{}, invalidBody()
	}
}

func decodeInheritedPatch[T any](fields map[string]json.RawMessage) (sourcetransport.PatchField[T], error) {
	if len(fields) != 1 {
		return sourcetransport.PatchField[T]{}, invalidBody()
	}
	return sourcetransport.Clear[T](), nil
}

func decodeOverridePatch[T any](fields map[string]json.RawMessage, valid func(T) bool) (sourcetransport.PatchField[T], error) {
	if len(fields) != 2 {
		return sourcetransport.PatchField[T]{}, invalidBody()
	}
	rawValue, ok := fields["value"]
	if !ok {
		return sourcetransport.PatchField[T]{}, invalidBody()
	}
	var value *T
	if err := json.Unmarshal(rawValue, &value); err != nil || value == nil {
		return sourcetransport.PatchField[T]{}, invalidBody()
	}
	if valid != nil && !valid(*value) {
		return sourcetransport.PatchField[T]{}, invalidBody()
	}
	return sourcetransport.Set(*value), nil
}

func decodeRawObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, invalidBody()
	}
	return fields, nil
}

func patchMode(fields map[string]json.RawMessage) (string, error) {
	raw, ok := fields["mode"]
	if !ok {
		return "", invalidBody()
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err != nil {
		return "", invalidBody()
	}
	return mode, nil
}

func invalidBody() error {
	return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
}
