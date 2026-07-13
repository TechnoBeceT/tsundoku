package tracker

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// pkceVerifierBytes is the amount of randomness fed into
// GeneratePKCEVerifier. 32 raw bytes base64url-encodes (no padding) to a
// 43-character string.
const pkceVerifierBytes = 32

// GeneratePKCEVerifier returns a cryptographically random RFC 7636 PKCE code
// verifier: base64url (no padding) of pkceVerifierBytes random bytes,
// yielding a 43-character string. RFC 7636 requires 43-128 characters drawn
// from the unreserved charset [A-Za-z0-9-._~]; base64url's alphabet
// (A-Za-z0-9-_) is a strict subset of that, so every output is valid by
// construction — no separate charset check is needed.
//
// Tsundoku's ONLY OAuth-PKCE tracker (MAL) uses the PLAIN transform:
// code_challenge == this verifier verbatim, no code_challenge_method sent
// (see mal.Client.AuthURL) and no client secret — this generator lives here
// (not package mal) so a later PKCE tracker can reuse it without an import
// cycle.
func GeneratePKCEVerifier() (string, error) {
	buf := make([]byte, pkceVerifierBytes)
	if _, err := rand.Read(buf); err != nil {
		// Defensive path: crypto/rand.Read only fails if the OS entropy
		// source is unavailable, which does not happen on any platform
		// this codebase targets; unreachable in practice.
		return "", fmt.Errorf("tracker: generate pkce verifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
