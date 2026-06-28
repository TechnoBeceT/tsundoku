package auth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/technobecet/tsundoku/internal/pkg/auth"
)

func newTestService() *auth.Service {
	return auth.NewService("test-secret-key-for-testing-only")
}

func TestHashPassword_DiffersFromPlaintext(t *testing.T) {
	svc := newTestService()
	pw := "supersecret"
	hash, err := svc.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: unexpected error: %v", err)
	}
	if hash == pw {
		t.Error("HashPassword: hash must differ from plaintext")
	}
}

func TestVerifyPassword_Correct(t *testing.T) {
	svc := newTestService()
	pw := "correct-horse-battery-staple"
	hash, err := svc.HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: unexpected error: %v", err)
	}
	if !svc.VerifyPassword(hash, pw) {
		t.Error("VerifyPassword: expected true for correct password")
	}
}

func TestVerifyPassword_Wrong(t *testing.T) {
	svc := newTestService()
	hash, err := svc.HashPassword("correct-password")
	if err != nil {
		t.Fatalf("HashPassword: unexpected error: %v", err)
	}
	if svc.VerifyPassword(hash, "wrong-password") {
		t.Error("VerifyPassword: expected false for wrong password")
	}
}

func TestIssueValidate_RoundTrip(t *testing.T) {
	svc := newTestService()
	id := uuid.New()
	tok, err := svc.Issue(id)
	if err != nil {
		t.Fatalf("Issue: unexpected error: %v", err)
	}
	claims, err := svc.Validate(tok)
	if err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}
	if claims.OwnerID != id {
		t.Errorf("Validate: got OwnerID %v, want %v", claims.OwnerID, id)
	}
}

func TestValidate_TamperedToken(t *testing.T) {
	svc := newTestService()
	tok, err := svc.Issue(uuid.New())
	if err != nil {
		t.Fatalf("Issue: unexpected error: %v", err)
	}
	// Flip the last byte of the token.
	tampered := []byte(tok)
	tampered[len(tampered)-1] ^= 0x01
	_, err = svc.Validate(string(tampered))
	if err == nil {
		t.Error("Validate: expected error for tampered token, got nil")
	}
}

func TestValidate_ExpiredToken(t *testing.T) {
	svc := newTestService()
	id := uuid.New()
	// Issue a token that expired in the past.
	tok, err := auth.IssueWithExpiry(svc, id, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("IssueWithExpiry: unexpected error: %v", err)
	}
	_, err = svc.Validate(tok)
	if err == nil {
		t.Error("Validate: expected error for expired token, got nil")
	}
}

func TestService_ShouldRenew(t *testing.T) {
	svc := auth.NewService("0123456789abcdef0123456789abcdef")
	id := uuid.New()
	tok, err := svc.Issue(id)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := svc.Validate(tok)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Fresh token: not past half-life.
	if svc.ShouldRenew(claims, claims.IssuedAt.Add(time.Hour)) {
		t.Fatal("fresh token should not need renewal")
	}
	// Past the 15-day half-life: needs renewal.
	if !svc.ShouldRenew(claims, claims.IssuedAt.Add(16*24*time.Hour)) {
		t.Fatal("token past half-life should need renewal")
	}
}
