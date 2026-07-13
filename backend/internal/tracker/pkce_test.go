package tracker_test

import (
	"regexp"
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker"
)

// rfc7636PlainCharset matches RFC 7636's unreserved code-verifier charset:
// [A-Za-z0-9-._~], 43-128 characters.
var rfc7636PlainCharset = regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)

// TestGeneratePKCEVerifier_LengthAndCharset pins the shape mal.Client.AuthURL
// depends on for PKCE-plain (code_challenge == this verifier verbatim): the
// output must sit inside RFC 7636's 43-128 length window and use only its
// unreserved charset.
func TestGeneratePKCEVerifier_LengthAndCharset(t *testing.T) {
	v, err := tracker.GeneratePKCEVerifier()
	if err != nil {
		t.Fatalf("GeneratePKCEVerifier: %v", err)
	}
	if !rfc7636PlainCharset.MatchString(v) {
		t.Fatalf("GeneratePKCEVerifier() = %q, does not match RFC 7636 plain charset/length", v)
	}
}

// TestGeneratePKCEVerifier_Unique confirms two calls never collide — a
// deterministic/reused verifier would let one login's PKCE proof be replayed
// against another.
func TestGeneratePKCEVerifier_Unique(t *testing.T) {
	a, err := tracker.GeneratePKCEVerifier()
	if err != nil {
		t.Fatalf("GeneratePKCEVerifier: %v", err)
	}
	b, err := tracker.GeneratePKCEVerifier()
	if err != nil {
		t.Fatalf("GeneratePKCEVerifier: %v", err)
	}
	if a == b {
		t.Fatalf("GeneratePKCEVerifier produced the same value twice: %q", a)
	}
}
