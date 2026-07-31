package imports

import (
	"context"
	"log/slog"
	"time"
)

// coverageFastPath is how long a request will WAIT for a fresh computation
// before answering `pending`. Short by design: it exists so a small series
// still feels synchronous (its whole walk takes well under a second), not to
// give a large one a chance to finish.
const coverageFastPath = 3 * time.Second

// Coverage returns the per-scanlator breakdown for (sourceID, url), the
// entry point GAP-140 gives the HTTP handler in place of the old unbounded
// SourceBreakdown walk.
//
// A READY snapshot is served immediately — no engine call, no wait. Otherwise
// the computation is started on a DETACHED context (context.WithoutCancel —
// the walk must NOT die the instant this request's own context is torn down,
// which is exactly why a slow computation used to be unrecoverable) and the
// caller waits at most coverageFastPath for it. Small series therefore behave
// exactly as before (their whole walk finishes well inside the window); only
// expensive ones fall through to `pending`, with imports.coverage.done
// delivering the result when it lands.
//
// A ComputeCoverage failure is deliberately NOT surfaced as an error return
// here: every exit path of ComputeCoverage already persists a `failed`
// snapshot with its reason and announces imports.coverage.done (see its doc
// comment), so a caller sees the failure as an ordinary CoverageSnapshot with
// Status == coverageStatusFailed, not as an HTTP-level error. The error this
// function DOES return is reserved for a genuine store failure — loadCoverage
// itself unable to read back what ComputeCoverage just wrote.
func (s *Service) Coverage(ctx context.Context, sourceID, url, mangaTitle string) (CoverageSnapshot, error) {
	snap, ok, err := s.loadCoverage(ctx, sourceID, url)
	if err != nil {
		return CoverageSnapshot{}, err
	}
	if ok && snap.Status == coverageStatusReady {
		return snap, nil
	}

	done := make(chan struct{})
	// context.WithoutCancel: the computation must NOT die when this request
	// returns, which is the entire reason a slow walk was unrecoverable before
	// (GAP-140) — a plain ctx here would cancel the walk the instant the HTTP
	// handler's request context is torn down, right after the fast-path select
	// below falls through to `pending`.
	bg := context.WithoutCancel(ctx)
	go func() {
		defer close(done)
		if err := s.ComputeCoverage(bg, sourceID, url, mangaTitle); err != nil {
			slog.WarnContext(bg, "imports.Coverage: background computation failed",
				"source_id", sourceID, "manga_url", url, "err", err)
		}
	}()

	select {
	case <-done:
		fresh, _, err := s.loadCoverage(ctx, sourceID, url)
		return fresh, err
	case <-time.After(coverageFastPath):
		return CoverageSnapshot{Status: coverageStatusPending}, nil
	}
}
