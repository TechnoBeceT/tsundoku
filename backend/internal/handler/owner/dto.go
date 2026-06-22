// Package owner contains the HTTP handlers for the single-owner claim and
// login endpoints.
package owner

// ClaimRequest is the request body for POST /api/owner/claim.
type ClaimRequest struct {
	// Username is the desired owner username.
	Username string `json:"username"`
	// Password is the desired owner password (min 8 chars).
	Password string `json:"password"`
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
