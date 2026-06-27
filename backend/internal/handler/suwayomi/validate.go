package suwayomi

import (
	"strconv"

	"github.com/technobecet/tsundoku/internal/handler/httperr"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
	suwayomicli "github.com/technobecet/tsundoku/internal/suwayomi"
)

// UpdateRequest is the PATCH /api/suwayomi/settings body. Both groups and every
// field within them are optional pointers: a nil group or field is left
// untouched, so the request is a partial update that never clobbers an unset
// setting. At least one field must be present (an empty body is a 400).
type UpdateRequest struct {
	// FlareSolverr, when present, carries FlareSolverr field updates.
	FlareSolverr *FlareSolverrUpdate `json:"flareSolverr"`
	// SocksProxy, when present, carries SOCKS-proxy field updates.
	SocksProxy *SocksProxyUpdate `json:"socksProxy"`
}

// FlareSolverrUpdate is the partial FlareSolverr group; nil fields are untouched.
type FlareSolverrUpdate struct {
	Enabled            *bool   `json:"enabled"`
	URL                *string `json:"url"`
	Timeout            *int    `json:"timeout"`
	SessionName        *string `json:"sessionName"`
	SessionTTL         *int    `json:"sessionTtl"`
	AsResponseFallback *bool   `json:"asResponseFallback"`
}

// SocksProxyUpdate is the partial SOCKS-proxy group; nil fields are untouched.
type SocksProxyUpdate struct {
	Enabled  *bool   `json:"enabled"`
	Version  *int    `json:"version"`
	Host     *string `json:"host"`
	Port     *string `json:"port"`
	Username *string `json:"username"`
	Password *string `json:"password"`
}

// maxPort is the highest valid TCP port.
const maxPort = 65535

// validateUpdate fail-closes the request and maps it to a client-level patch.
// Rules:
//   - at least one field must be provided (empty body → 400);
//   - FlareSolverr URL, when set non-empty, must be an absolute http/https URL
//     with a host (an empty string is allowed and clears the URL);
//   - FlareSolverr timeout / sessionTtl, when set, must be non-negative;
//   - SOCKS version, when set, must be 4 or 5;
//   - SOCKS port, when set, must parse as an integer in 1..65535.
//
// On any violation it returns a 400 echo.HTTPError naming the offending field.
// Per-group validation is split out so each unit stays small (one job per func).
func validateUpdate(req UpdateRequest) (suwayomicli.SuwayomiSettingsPatch, error) {
	var patch suwayomicli.SuwayomiSettingsPatch
	if err := applyFlareSolverr(req.FlareSolverr, &patch); err != nil {
		return patch, err
	}
	if err := applySocks(req.SocksProxy, &patch); err != nil {
		return patch, err
	}
	// IsEmpty rejects an absent or all-empty body (e.g. {} or {"socksProxy":{}}).
	if patch.IsEmpty() {
		return patch, httperr.BadRequest("at least one setting must be provided")
	}
	return patch, nil
}

// applyFlareSolverr validates the FlareSolverr group and copies its set fields
// onto patch. A nil pointer field is copied as-is (nil → untouched), so only the
// constraints (URL well-formedness, non-negative timeouts) need branches.
func applyFlareSolverr(fs *FlareSolverrUpdate, patch *suwayomicli.SuwayomiSettingsPatch) error {
	if fs == nil {
		return nil
	}
	if fs.URL != nil {
		if err := validateOptionalURL(*fs.URL); err != nil {
			return err
		}
	}
	if fs.Timeout != nil && *fs.Timeout < 0 {
		return httperr.BadRequest("flareSolverr.timeout must be non-negative")
	}
	if fs.SessionTTL != nil && *fs.SessionTTL < 0 {
		return httperr.BadRequest("flareSolverr.sessionTtl must be non-negative")
	}
	patch.FlareSolverrEnabled = fs.Enabled
	patch.FlareSolverrURL = fs.URL
	patch.FlareSolverrTimeout = fs.Timeout
	patch.FlareSolverrSessionName = fs.SessionName
	patch.FlareSolverrSessionTTL = fs.SessionTTL
	patch.FlareSolverrAsResponseFallback = fs.AsResponseFallback
	return nil
}

// applySocks validates the SOCKS-proxy group and copies its set fields onto
// patch (same nil-is-untouched copy rule as applyFlareSolverr).
func applySocks(sp *SocksProxyUpdate, patch *suwayomicli.SuwayomiSettingsPatch) error {
	if sp == nil {
		return nil
	}
	if sp.Version != nil && *sp.Version != 4 && *sp.Version != 5 {
		return httperr.BadRequest("socksProxy.version must be 4 or 5")
	}
	if sp.Port != nil {
		if err := validatePort(*sp.Port); err != nil {
			return err
		}
	}
	patch.SocksProxyEnabled = sp.Enabled
	patch.SocksProxyVersion = sp.Version
	patch.SocksProxyHost = sp.Host
	patch.SocksProxyPort = sp.Port
	patch.SocksProxyUsername = sp.Username
	patch.SocksProxyPassword = sp.Password
	return nil
}

// validateOptionalURL accepts an empty string (clears the URL) or a well-formed
// absolute http/https URL with a host. Anything else is a 400. The
// absolute-http(s) check is the shared urlx.IsAbsoluteHTTP kernel (reused by the
// extension-repo validator) so the rule lives in exactly one place.
func validateOptionalURL(raw string) error {
	if raw == "" {
		return nil
	}
	if !urlx.IsAbsoluteHTTP(raw) {
		return httperr.BadRequest("flareSolverr.url must be a valid absolute http(s) URL")
	}
	return nil
}

// validatePort accepts a SOCKS port as a numeric string in 1..65535. Suwayomi
// stores the port as a String, so the value is validated as a port range here.
func validatePort(raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPort {
		return httperr.BadRequest("socksProxy.port must be a number in 1..65535")
	}
	return nil
}
