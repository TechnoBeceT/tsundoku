package runtimepolicy

import (
	"errors"
	"fmt"
)

// KCEFPolicy controls whether a source needs the embedded Chromium WebView.
// Auto inherits the route-derived capability, Required forces it on when that
// route can support it, and Disabled keeps it off.
type KCEFPolicy string

const (
	KCEFPolicyAuto     KCEFPolicy = "auto"
	KCEFPolicyRequired KCEFPolicy = "required"
	KCEFPolicyDisabled KCEFPolicy = "disabled"
)

var (
	// ErrKCEFWithSocks reports a policy that would require Chromium over a JVM
	// SOCKS route, which Chromium cannot honor.
	ErrKCEFWithSocks = errors.New("required embedded browser cannot use SOCKS")
	// ErrInvalidKCEFPolicy reports a value outside the persisted policy enum.
	ErrInvalidKCEFPolicy = errors.New("invalid embedded browser policy")
)

// ResolveKCEF returns the effective embedded-browser capability for one
// normalized route. Blank and unknown flare modes follow the existing global
// route default; required plus SOCKS is rejected before a runtime mutation can
// commit an impossible source configuration.
func ResolveKCEF(policy KCEFPolicy, hasSocks bool, flareMode string) (bool, error) {
	switch policy {
	case KCEFPolicyAuto, KCEFPolicyRequired, KCEFPolicyDisabled:
	default:
		return false, fmt.Errorf("%w: %q", ErrInvalidKCEFPolicy, policy)
	}

	if hasSocks {
		if policy == KCEFPolicyRequired {
			return false, ErrKCEFWithSocks
		}
		return false, nil
	}

	switch policy {
	case KCEFPolicyAuto:
		return flareMode != "endpoint", nil
	case KCEFPolicyRequired:
		return true, nil
	case KCEFPolicyDisabled:
		return false, nil
	}
	return false, fmt.Errorf("%w: %q", ErrInvalidKCEFPolicy, policy)
}
