package enginehost_test

import (
	"fmt"
	"net"
	"testing"

	"github.com/technobecet/tsundoku/internal/enginehost"
)

// TestAllocFreePort_ReturnsUsablePort proves the allocator returns a positive
// port that can actually be bound.
func TestAllocFreePort_ReturnsUsablePort(t *testing.T) {
	port, err := enginehost.AllocFreePort()
	if err != nil {
		t.Fatalf("AllocFreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("AllocFreePort = %d, want a valid TCP port", port)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("allocated port %d is not bindable: %v", port, err)
	}
	_ = ln.Close()
}

// TestAllocFreePort_AvoidsLivePort proves the allocator never hands back a port a
// live instance is already holding — the real uniqueness guarantee (a spawned
// JVM keeps its port bound, so the next allocation gets a different one).
func TestAllocFreePort_AvoidsLivePort(t *testing.T) {
	first, err := enginehost.AllocFreePort()
	if err != nil {
		t.Fatalf("AllocFreePort #1: %v", err)
	}
	// Simulate the first instance holding its port for its whole lifetime.
	held, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", first))
	if err != nil {
		t.Fatalf("hold port %d: %v", first, err)
	}
	defer func() { _ = held.Close() }()

	second, err := enginehost.AllocFreePort()
	if err != nil {
		t.Fatalf("AllocFreePort #2: %v", err)
	}
	if second == first {
		t.Errorf("AllocFreePort reused a port a live instance holds: %d", second)
	}
}
