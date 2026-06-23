// Package owner contains the HTTP handlers for the single-owner claim and
// login endpoints.
package owner

import "errors"

// minPasswordLen is the minimum number of bytes required for a claim password,
// matching the minLength:8 constraint declared in the OpenAPI ClaimRequest schema.
const minPasswordLen = 8

// ClaimRequest is the request body for POST /api/owner/claim.
type ClaimRequest struct {
	// Username is the desired owner username.
	Username string `json:"username"`
	// Password is the desired owner password (min 8 bytes).
	Password string `json:"password"`
}

// validate checks that the claim request fields satisfy the API contract.
// It returns an error describing the first violation found, suitable for
// surfacing directly to the caller via the central error middleware.
func (r ClaimRequest) validate() error {
	if r.Username == "" {
		return errors.New("username is required")
	}
	if len(r.Password) < minPasswordLen {
		return errors.New("password must be at least 8 bytes")
	}
	return nil
}

// LoginRequest is the request body for POST /api/owner/login.
type LoginRequest struct {
	// Username is the owner's username.
	Username string `json:"username"`
	// Password is the owner's password.
	Password string `json:"password"`
}

// TokenResponse is the response body for both claim and login on success.
type TokenResponse struct {
	// Token is a signed Bearer token for the authenticated owner.
	Token string `json:"token"`
}
