package auth

import (
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

// Service holds the HMAC secret and provides password hashing and token
// issuance/validation for the Tsundoku single-owner auth layer.
type Service struct {
	secret []byte
}

// NewService constructs a Service that uses the given secret for HMAC signing.
func NewService(secret string) *Service {
	return &Service{secret: []byte(secret)}
}

// HashPassword hashes pw using bcrypt at cost 12 and returns the hash string.
func (s *Service) HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// VerifyPassword compares a bcrypt hash against a plaintext password.
// It returns true if they match and false otherwise (never an error).
func (s *Service) VerifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}
