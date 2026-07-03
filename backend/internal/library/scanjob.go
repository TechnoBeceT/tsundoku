package library

import "context"

// StartScan launches a library scan in the background, streaming scan.start /
// scan.progress / scan.done over the SSE hub, and returns immediately. It
// exists because a synchronous scan over a 1000+ series NFS library can run
// long enough to trip an HTTP-gateway timeout (e.g. a Cloudflare Tunnel's
// 100s edge limit) — the owner instead gets a 202 and live progress.
//
// Returns false without starting a new scan if one is already in flight
// (single-flight guard, held by scanMu/scanning): a double-click on "Scan"
// must not launch two concurrent NFS walks racing to upsert the same
// ImportEntry rows. The scan itself is upsert-only (see scan.go) so even a
// theoretical overlap would be safe, but running two full walks
// simultaneously wastes NFS/DB round trips for no benefit.
func (s *Service) StartScan(ctx context.Context) bool {
	s.scanMu.Lock()
	if s.scanning {
		s.scanMu.Unlock()
		return false
	}
	s.scanning = true
	s.scanMu.Unlock()

	go func() {
		defer func() {
			s.scanMu.Lock()
			s.scanning = false
			s.scanMu.Unlock()
		}()

		s.broadcastScan("scan.start", ScanEvent{})

		// The goroutine outlives the HTTP request that triggered it (StartScan
		// already returned by the time the scan finishes), so it must not
		// inherit that request's cancellation — context.WithoutCancel strips
		// the Done channel/deadline while keeping any request-scoped values.
		// Without this, the scan would abort as soon as Echo returns the 202
		// response and its request context is cancelled.
		found, err := s.scanWithProgress(context.WithoutCancel(ctx))
		if err != nil {
			s.broadcastScan("scan.done", ScanEvent{Error: err.Error()})
			return
		}
		s.broadcastScan("scan.done", ScanEvent{Total: len(found), Found: len(found)})
	}()
	return true
}
