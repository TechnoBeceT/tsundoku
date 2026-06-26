// Package urlx holds small, dependency-free URL-validation helpers shared by the
// HTTP handlers so the same rule never gets copy-pasted (and silently drifts)
// across packages.
package urlx

import "net/url"

// IsAbsoluteHTTP reports whether raw is an absolute http or https URL with a
// non-empty host. It is the single shared kernel behind both the FlareSolverr-URL
// validator (handler/suwayomi) and the extension-repo-URL validator
// (handler/extensions), so "valid absolute http(s) URL" is defined in exactly one
// place. It does NOT accept the empty string — callers that allow an empty value
// (e.g. "clear this field") must short-circuit on "" themselves before calling.
func IsAbsoluteHTTP(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return true
}
