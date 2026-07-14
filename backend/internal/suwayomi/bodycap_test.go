// Package suwayomi_test — unit tests for the PageBytes body-size cap.
//
// The cap bounds how much of a page/cover response is read into memory; an over-cap
// body errors (rather than being silently truncated into a clipped image). Tested at
// the readAllLimited helper with a tiny limit so no multi-MB payload is allocated.
package suwayomi_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/suwayomi"
)

// TestReadAllLimited_UnderCap verifies a body at or under the limit is returned whole.
func TestReadAllLimited_UnderCap(t *testing.T) {
	t.Parallel()

	body := []byte("hello") // 5 bytes
	got, err := suwayomi.ReadAllLimited(bytes.NewReader(body), 5)
	if err != nil {
		t.Fatalf("under-cap read: unexpected error: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("under-cap read: got %q, want %q", got, body)
	}
}

// TestReadAllLimited_OverCap verifies an over-cap body is REJECTED with an error
// (never silently truncated — a clipped image would decode as a broken panel).
func TestReadAllLimited_OverCap(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte{0xAB}, 100)
	_, err := suwayomi.ReadAllLimited(bytes.NewReader(body), 10)
	if err == nil {
		t.Fatal("over-cap body accepted, want an error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("over-cap error = %v, want it to mention the size cap", err)
	}
}
