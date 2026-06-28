// Package auth provides password hashing and signed-token issuance/validation
// for the single-owner Tsundoku authentication layer.
package auth

import (
	"time"

	"github.com/google/uuid"
)

// Claims carries the verified identity extracted from a valid token.
// OwnerID is the UUID primary key of the authenticated Owner row.
// IssuedAt and ExpiresAt record when the token was minted and when it expires.
type Claims struct {
	OwnerID   uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
}
