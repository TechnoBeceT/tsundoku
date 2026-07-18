package enginehost

import (
	"fmt"
	"net"
)

// allocFreePort is the production PortAllocator: it binds 127.0.0.1:0, reads the
// kernel-assigned free port, then closes the listener and returns the port for a
// fresh engine-host instance to bind.
//
// There is an inherent (small) TOCTOU window — another process could grab the
// port between the close here and the JVM binding it — but the loopback
// ephemeral range is large and this launcher spawns only a handful of instances
// on a single-owner host, so a collision is vanishingly unlikely; if it ever
// happens the JVM fails to bind, the spawn's health-poll times out, and that
// profile degrades to the default instance (fault isolation), then the next
// reconcile retries with a fresh port.
func allocFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("enginehost: allocate free port: %w", err)
	}
	defer func() { _ = ln.Close() }()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		// Unreachable: net.Listen("tcp", …) always yields a *net.TCPAddr. Kept as
		// a guard rather than a panic in case the stdlib contract ever changes.
		return 0, fmt.Errorf("enginehost: unexpected listener address type %T", ln.Addr())
	}
	return addr.Port, nil
}
