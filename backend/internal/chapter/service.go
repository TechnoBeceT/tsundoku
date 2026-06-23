// Package chapter contains the chapter domain: state machine, ingest, and
// service helpers used by the M1 download dispatcher and upgrade engine.
package chapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// WantedChapters returns up to limit Chapter rows that are actionable by the
// download dispatcher. Actionable means:
//   - state == wanted, OR
//   - state == failed AND retries < maxRetries AND (next_attempt_at IS NULL OR next_attempt_at <= now)
//
// upgrade_available chapters are deliberately excluded — Task 6's upgrade engine
// handles that path. Results are ordered by ID ascending (deterministic proxy
// for creation order).
func WantedChapters(ctx context.Context, client *ent.Client, limit int, maxRetries int) ([]*ent.Chapter, error) {
	chapters, err := client.Chapter.Query().
		Where(entchapter.Or(
			entchapter.StateEQ(entchapter.StateWanted),
			entchapter.And(
				entchapter.StateEQ(entchapter.StateFailed),
				entchapter.RetriesLT(maxRetries),
				entchapter.Or(
					entchapter.NextAttemptAtIsNil(),
					entchapter.NextAttemptAtLTE(time.Now()),
				),
			),
		)).
		Order(entchapter.ByID()).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("chapter.WantedChapters: query: %w", err)
	}
	return chapters, nil
}

// BestProviderChapter returns the ProviderChapter for chapterID that is on the
// highest-importance SeriesProvider offering this chapter's key. The
// SeriesProvider must belong to the same series as the Chapter. A higher
// importance number means higher priority / quality.
//
// Returns an error if no ProviderChapter offers this chapter's key for this
// series, or if the chapter cannot be loaded. The second return value is the
// importance of the selected SeriesProvider.
func BestProviderChapter(ctx context.Context, client *ent.Client, chapterID uuid.UUID) (*ent.ProviderChapter, int, error) {
	ch, err := client.Chapter.Get(ctx, chapterID)
	if err != nil {
		return nil, 0, fmt.Errorf("chapter.BestProviderChapter: load chapter %s: %w", chapterID, err)
	}

	pcs, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.ChapterKeyEQ(ch.ChapterKey),
			entproviderchapter.HasSeriesProviderWith(
				entseriesprovider.SeriesIDEQ(ch.SeriesID),
			),
		).
		WithSeriesProvider().
		All(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("chapter.BestProviderChapter: query provider chapters for chapter %s: %w", chapterID, err)
	}

	if len(pcs) == 0 {
		return nil, 0, fmt.Errorf("chapter.BestProviderChapter: no provider chapter found for chapter %s (key=%q)", chapterID, ch.ChapterKey)
	}

	var best *ent.ProviderChapter
	bestImportance := -1
	for _, pc := range pcs {
		sp := pc.Edges.SeriesProvider
		if sp == nil {
			// Defensive path: WithSeriesProvider always loads the edge when the FK
			// is valid, so a nil SeriesProvider here means a missing FK — not
			// reachable under normal operation.
			continue
		}
		if sp.Importance > bestImportance {
			bestImportance = sp.Importance
			best = pc
		}
	}

	if best == nil {
		return nil, 0, fmt.Errorf("chapter.BestProviderChapter: no provider chapter with loaded series_provider for chapter %s", chapterID)
	}

	return best, bestImportance, nil
}
