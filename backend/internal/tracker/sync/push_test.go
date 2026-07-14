package sync_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/tracker/sync"
)

// TestNextPush pins the never-regress rule: a push is warranted ONLY when
// local is STRICTLY greater than remote. This is non-vacuous against the
// opposite bug (e.g. pushing on >=, or always pushing local regardless of
// remote) — each case below would fail under either mutation.
func TestNextPush(t *testing.T) {
	tests := []struct {
		name           string
		local, remote  float64
		wantPush       float64
		wantShouldPush bool
	}{
		{"local greater than remote pushes local", 50, 30, 50, true},
		{"local equal to remote does not push", 30, 30, 0, false},
		{"local less than remote does not push", 10, 30, 0, false},
		{"zero local, zero remote does not push", 0, 0, 0, false},
		{"zero local, positive remote does not push", 0, 5, 0, false},
		{"positive local, zero remote pushes local", 5, 0, 5, true},
		{"fractional local greater than remote pushes fractional value", 12.5, 12, 12.5, true},
		{"fractional local equal to remote does not push", 12.5, 12.5, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			push, shouldPush := sync.NextPush(tt.local, tt.remote)
			if shouldPush != tt.wantShouldPush {
				t.Fatalf("NextPush(%v, %v) shouldPush = %v, want %v", tt.local, tt.remote, shouldPush, tt.wantShouldPush)
			}
			if shouldPush && push != tt.wantPush {
				t.Fatalf("NextPush(%v, %v) push = %v, want %v", tt.local, tt.remote, push, tt.wantPush)
			}
		})
	}
}

// TestNextPush_NeverPushesLowerThanRemote is the adversarial proof the
// spec's rule exists for: even when local is nonzero and "looks like real
// progress", any local <= remote must never push — a regression here (e.g.
// flipping the comparison to >=) would silently drag a tracker's progress
// backward, which is the exact failure this kernel exists to prevent.
func TestNextPush_NeverPushesLowerThanRemote(t *testing.T) {
	for _, remote := range []float64{1, 10, 100, 12.5} {
		for _, local := range []float64{0, remote - 0.1, remote} {
			if local < 0 {
				continue
			}
			_, shouldPush := sync.NextPush(local, remote)
			if shouldPush {
				t.Fatalf("NextPush(%v, %v) should never push (local <= remote), got shouldPush=true", local, remote)
			}
		}
	}
}
