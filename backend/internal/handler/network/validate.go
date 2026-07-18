// Package network contains the thin HTTP handlers for the per-source
// network-routing API (QCAT-283): CRUD over reusable egress endpoints (a SOCKS
// proxy or a FlareSolverr instance) and the per-source binding assignment.
// Business logic + validation live in internal/network (the service); these
// handlers only bind/parse the request, map it to a service input, call the
// service, and render the DTO. The service package internal/network collides
// with this package name, so it is imported aliased (networksvc) in handler.go.
package network

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	networksvc "github.com/technobecet/tsundoku/internal/network"
)

// CreateEndpointRequest is the POST /api/network/endpoints body — the full field
// set for a new endpoint. name + kind are required; the SOCKS group applies when
// kind == "socks", the FlareSolverr group when kind == "flaresolverr" (the
// service validates by kind). Two fields default when omitted (zero-valued) to
// match the Ent column defaults: socksVersion → 5, timeout → 60.
type CreateEndpointRequest struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Enabled      *bool  `json:"enabled"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	SocksVersion int    `json:"socksVersion"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	URL          string `json:"url"`
	FSProxy      string `json:"fsProxy"`
	Session      string `json:"session"`
	SessionTTL   int    `json:"sessionTtl"`
	Timeout      int    `json:"timeout"`
}

// toInput maps the request to the service EndpointInput, applying the two
// column-default fallbacks (socksVersion → 5, timeout → 60) and defaulting
// enabled to true when the field is omitted.
func (r CreateEndpointRequest) toInput() networksvc.EndpointInput {
	socksVersion := r.SocksVersion
	if socksVersion == 0 {
		socksVersion = 5
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 60
	}
	enabled := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	return networksvc.EndpointInput{
		Name:         r.Name,
		Kind:         r.Kind,
		Enabled:      enabled,
		Host:         r.Host,
		Port:         r.Port,
		SocksVersion: socksVersion,
		Username:     r.Username,
		Password:     r.Password,
		URL:          r.URL,
		FSProxy:      r.FSProxy,
		Session:      r.Session,
		SessionTTL:   r.SessionTTL,
		Timeout:      r.Timeout,
	}
}

// UpdateEndpointRequest is the PATCH /api/network/endpoints/{id} body. Every
// field is an optional pointer: a nil field is left untouched. A nil password
// KEEPS the stored password (write-only — the frontend loads the edit form with
// a blank password field, so an untouched password must never clear it).
type UpdateEndpointRequest struct {
	Name         *string `json:"name"`
	Kind         *string `json:"kind"`
	Enabled      *bool   `json:"enabled"`
	Host         *string `json:"host"`
	Port         *int    `json:"port"`
	SocksVersion *int    `json:"socksVersion"`
	Username     *string `json:"username"`
	Password     *string `json:"password"`
	URL          *string `json:"url"`
	FSProxy      *string `json:"fsProxy"`
	Session      *string `json:"session"`
	SessionTTL   *int    `json:"sessionTtl"`
	Timeout      *int    `json:"timeout"`
}

// toPatch maps the request to the service EndpointPatch (a straight field copy —
// nil stays nil).
func (r UpdateEndpointRequest) toPatch() networksvc.EndpointPatch {
	return networksvc.EndpointPatch{
		Name:         r.Name,
		Kind:         r.Kind,
		Enabled:      r.Enabled,
		Host:         r.Host,
		Port:         r.Port,
		SocksVersion: r.SocksVersion,
		Username:     r.Username,
		Password:     r.Password,
		URL:          r.URL,
		FSProxy:      r.FSProxy,
		Session:      r.Session,
		SessionTTL:   r.SessionTTL,
		Timeout:      r.Timeout,
	}
}

// SetBindingRequest is the PUT /api/network/sources/{sourceId}/binding body. The
// two endpoint ids are optional stringified UUIDs (null / omitted = no override
// for that dimension); flareMode is required.
type SetBindingRequest struct {
	SocksEndpointID *string `json:"socksEndpointId"`
	FlareMode       string  `json:"flareMode"`
	FlareEndpointID *string `json:"flareEndpointId"`
}

// toInput maps the request to the service BindingInput, parsing the optional
// endpoint UUIDs. A malformed UUID is a 400.
func (r SetBindingRequest) toInput() (networksvc.BindingInput, error) {
	socks, err := parseOptionalUUID(r.SocksEndpointID, "socksEndpointId")
	if err != nil {
		return networksvc.BindingInput{}, err
	}
	flare, err := parseOptionalUUID(r.FlareEndpointID, "flareEndpointId")
	if err != nil {
		return networksvc.BindingInput{}, err
	}
	return networksvc.BindingInput{
		SocksEndpointID: socks,
		FlareMode:       r.FlareMode,
		FlareEndpointID: flare,
	}, nil
}

// validateID parses an endpoint UUID path param, yielding a 400 on a malformed
// value.
func validateID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, echo.NewHTTPError(http.StatusBadRequest, "invalid endpoint id")
	}
	return id, nil
}

// parseSourceID parses the :sourceId path param as a decimal int64 — the
// engine-host source identity a binding is keyed by. A blank or non-numeric
// value yields a 400.
func parseSourceID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "sourceId must be numeric")
	}
	return id, nil
}

// parseOptionalUUID parses an optional stringified UUID from a request body. A
// nil or empty value yields (nil, nil) — no override; a malformed value yields a
// 400 naming the field.
func parseOptionalUUID(raw *string, field string) (*uuid.UUID, error) {
	if raw == nil {
		return nil, nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil, nil
	}
	id, err := uuid.Parse(trimmed)
	if err != nil {
		return nil, echo.NewHTTPError(http.StatusBadRequest, field+" must be a valid UUID")
	}
	return &id, nil
}
