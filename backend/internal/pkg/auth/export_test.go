package auth

import (
	"time"

	"github.com/google/uuid"
)

// IssueWithExpiry issues a token with a custom expiry for testing only.
func IssueWithExpiry(svc *Service, id uuid.UUID, exp time.Time) (string, error) {
	return svc.issueWithExpiry(id, exp)
}
