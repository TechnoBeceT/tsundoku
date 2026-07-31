package imports

import (
	"context"
	"fmt"
	"log/slog"
)

// ComputeCoverage runs ONE per-scanlator coverage computation to completion
// and persists the result (GAP-140).
//
// It is deliberately synchronous — the caller decides whether to detach. The
// expensive part is SourceBreakdown's chapter-list walk, which since the JS
// Detection cutover costs one WebView navigation PER PAGE (~330 for a
// 1,301-chapter series). That is why the result is persisted rather than held
// in ingest.ChapterCache: an in-memory entry is discarded by any restart, and
// a discarded 20-minute walk puts the owner back where they started.
//
// Every exit path both PERSISTS and ANNOUNCES, so a client that stopped
// waiting still learns the outcome from imports.coverage.done.
func (s *Service) ComputeCoverage(ctx context.Context, sourceID, url, mangaTitle string) error {
	if err := s.markCoveragePending(ctx, sourceID, url); err != nil {
		return err
	}

	dto, err := s.SourceBreakdown(ctx, sourceID, url, mangaTitle)
	if err != nil {
		if saveErr := s.failCoverage(ctx, sourceID, url, err); saveErr != nil {
			slog.WarnContext(ctx, "imports.ComputeCoverage: could not persist the failure",
				"source_id", sourceID, "manga_url", url, "err", saveErr)
		}
		s.broadcastCoverageDone(ctx, CoverageDoneEvent{
			SourceID: sourceID, MangaURL: url, Status: coverageStatusFailed, Error: err.Error(),
		})
		return fmt.Errorf("imports.ComputeCoverage: %s %s: %w", sourceID, url, err)
	}

	if err := s.saveCoverage(ctx, sourceID, url, dto); err != nil {
		// The walk succeeded but the write itself failed (e.g. a DB hiccup) —
		// this is still a terminal failure of the computation from the
		// caller's point of view: nothing readable was persisted, so a
		// silent return here would leave the row stuck at "pending" forever
		// with NO event to tell a waiting client anything happened.
		// failCoverage is itself best-effort here (it can fail on the very
		// same broken connection); either way the client still gets the
		// announcement.
		if failErr := s.failCoverage(ctx, sourceID, url, err); failErr != nil {
			slog.WarnContext(ctx, "imports.ComputeCoverage: could not persist the save failure",
				"source_id", sourceID, "manga_url", url, "err", failErr)
		}
		s.broadcastCoverageDone(ctx, CoverageDoneEvent{
			SourceID: sourceID, MangaURL: url, Status: coverageStatusFailed, Error: err.Error(),
		})
		return err
	}
	s.broadcastCoverageDone(ctx, CoverageDoneEvent{
		SourceID: sourceID, MangaURL: url, Status: coverageStatusReady, Total: dto.Total,
	})
	return nil
}
