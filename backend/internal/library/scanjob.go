package library

import (
	"context"
	"time"
)

// scanTimeout bounds how long StartScan's single-flight latch (scanning) can
// be held for one scan. disk.ScanLibrary walks storage via a raw os.ReadDir,
// a syscall a Go context cannot abort — a hung NFS mount would otherwise
// block the scan goroutine forever, wedging the latch true and returning 409
// on every subsequent StartScan until the process is restarted. 30m is
// generous (a metadata-sidecar walk of even a 1000+ series library is fast;
// this only trips on a truly-wedged mount). A var, not a const, so a test can
// shrink it (see export_test.go SetScanTimeout).
var scanTimeout = 30 * time.Minute

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
		scanCtx := context.WithoutCancel(ctx)

		// scanWithProgress runs in its own inner goroutine so the outer
		// goroutine can race it against scanTimeout below: a hung
		// disk.ScanLibrary walk (uninterruptible os.ReadDir on a wedged NFS
		// mount) cannot be cancelled by a context, so the only way to bound
		// the single-flight latch is to stop WAITING on it, not to stop it.
		type scanResult struct {
			found []FoundSeriesDTO
			err   error
		}
		resultCh := make(chan scanResult, 1)
		go func() {
			found, err := s.scanWithProgress(scanCtx)
			resultCh <- scanResult{found: found, err: err}
		}()

		select {
		case res := <-resultCh:
			if res.err != nil {
				s.broadcastScan("scan.done", ScanEvent{Error: res.err.Error()})
				return
			}
			s.broadcastScan("scan.done", ScanEvent{Total: len(res.found), Found: len(res.found)})
		case <-time.After(scanTimeout):
			// The inner goroutine above is abandoned here: it keeps running
			// (and will leak forever if the walk is truly wedged) since Go
			// cannot interrupt a blocked syscall. That leak is accepted under
			// the single-owner homelab model — the alternative (no bound at
			// all) is strictly worse, silently wedging the single-flight
			// latch true and 409-ing every future scan until a process
			// restart. Emitting a terminal scan.done + releasing the latch
			// (via the defer above) lets the owner retry immediately.
			s.broadcastScan("scan.done", ScanEvent{Error: "scan timed out after " + scanTimeout.String()})
		}
	}()
	return true
}
