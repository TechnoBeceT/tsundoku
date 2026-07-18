package network

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// EndpointInput is the full set of endpoint fields for CreateEndpoint. Kind
// selects which field-group is validated; the other group's fields are stored
// as given (defaulted) but never validated or used.
type EndpointInput struct {
	Name         string
	Kind         string
	Enabled      bool
	Host         string
	Port         int
	SocksVersion int
	Username     string
	Password     string
	URL          string
	FSProxy      string
	Session      string
	SessionTTL   int
	Timeout      int
}

// EndpointPatch is the partial set of endpoint fields for UpdateEndpoint. Every
// field is a pointer: a nil field is left untouched. In particular a nil
// Password KEEPS the stored password (write-only — the frontend loads the edit
// form with a blank password, and an untouched password must not clear it).
type EndpointPatch struct {
	Name         *string
	Kind         *string
	Enabled      *bool
	Host         *string
	Port         *int
	SocksVersion *int
	Username     *string
	Password     *string
	URL          *string
	FSProxy      *string
	Session      *string
	SessionTTL   *int
	Timeout      *int
}

// BindingInput is the desired binding state for SetBinding.
type BindingInput struct {
	SocksEndpointID *uuid.UUID
	FlareMode       string
	FlareEndpointID *uuid.UUID
}

// validateEndpoint checks a fully-resolved endpoint's fields by kind, returning
// ErrInvalidEndpoint (wrapped with the offending field) on any failure. It is
// run on the final field values by BOTH create (the input) and update (the
// stored row merged with the patch), so the store never holds an invalid
// endpoint.
func validateEndpoint(in EndpointInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return fmt.Errorf("%w: name must not be blank", ErrInvalidEndpoint)
	}
	switch in.Kind {
	case KindSocks:
		return validateSocksFields(in)
	case KindFlareSolverr:
		return validateFlareSolverrFields(in)
	default:
		return fmt.Errorf("%w: kind must be %q or %q", ErrInvalidEndpoint, KindSocks, KindFlareSolverr)
	}
}

// validateSocksFields enforces the SOCKS field-group rules: a non-blank host, a
// port in 1..65535, and a SOCKS version of 4 or 5.
func validateSocksFields(in EndpointInput) error {
	if strings.TrimSpace(in.Host) == "" {
		return fmt.Errorf("%w: host must not be blank for a socks endpoint", ErrInvalidEndpoint)
	}
	if in.Port < 1 || in.Port > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidEndpoint)
	}
	if in.SocksVersion != 4 && in.SocksVersion != 5 {
		return fmt.Errorf("%w: socksVersion must be 4 or 5", ErrInvalidEndpoint)
	}
	return nil
}

// validateFlareSolverrFields enforces the FlareSolverr field-group rules: an
// absolute http(s) url and non-negative timeout / session TTL.
func validateFlareSolverrFields(in EndpointInput) error {
	if !urlx.IsAbsoluteHTTP(in.URL) {
		return fmt.Errorf("%w: url must be an absolute http(s) URL", ErrInvalidEndpoint)
	}
	if in.Timeout < 0 {
		return fmt.Errorf("%w: timeout must be >= 0", ErrInvalidEndpoint)
	}
	if in.SessionTTL < 0 {
		return fmt.Errorf("%w: sessionTtl must be >= 0", ErrInvalidEndpoint)
	}
	return nil
}

// validateFlareMode confirms flare_mode is one of the three allowed values and
// that flare_endpoint_id is present iff (and only iff) the mode is "endpoint".
func validateFlareMode(mode string, flareEndpointID *uuid.UUID) error {
	switch mode {
	case FlareModeNone, FlareModeGlobal:
		if flareEndpointID != nil {
			return fmt.Errorf("%w: flareEndpointId must be absent unless flareMode is %q", ErrInvalidBinding, FlareModeEndpoint)
		}
	case FlareModeEndpoint:
		if flareEndpointID == nil {
			return fmt.Errorf("%w: flareEndpointId is required when flareMode is %q", ErrInvalidBinding, FlareModeEndpoint)
		}
	default:
		return fmt.Errorf("%w: flareMode must be %q, %q, or %q", ErrInvalidBinding, FlareModeNone, FlareModeGlobal, FlareModeEndpoint)
	}
	return nil
}

// uuidPtrToStringPtr renders a nullable UUID as a nullable string for a DTO.
func uuidPtrToStringPtr(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}
