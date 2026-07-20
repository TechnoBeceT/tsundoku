package downloads

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
)

// ActiveSourceCounts returns how many chapters are CURRENTLY being fetched from
// each source, keyed by the canonical source NAME (breakerKey — the exact key
// sourcegate.Service.Snapshot uses, so the engine source-status strip can join the
// two without any per-source lookup). A chapter is attributed to the source that
// is really doing the fetch:
//
//   - downloading → the top live candidate (the highest-importance source whose
//     feed carries the chapter_key, ranked exactly as chapter.RankedLiveCandidates
//     ranks it — chapterSource's primary-source rule);
//   - upgrading   → the upgrade TARGET (the higher source the already-downloaded
//     chapter is converging to; its satisfier is where the CBZ came from, NOT what
//     the fetch is aimed at).
//
// It is the read-side half of GET /api/engine/sources (the busy sources; the caller
// joins the cooling ones from the breaker snapshot). It makes ZERO engine/source-ward
// calls and is NO-N+1: (1) ONE query for the downloading/upgrading chapters, (2) ONE
// batch query for their distinct series' providers (with feeds). Every attribution is
// then resolved in memory over those already-loaded feeds, reusing the SAME resolvers
// (chapterSource / upgradeTargetCarrier / newUpgradeTargetIndex / breakerKey) the
// activity List uses — so the strip and the list can never name a source differently.
func (s *Service) ActiveSourceCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.client.Chapter.Query().
		Where(entchapter.StateIn(entchapter.StateDownloading, entchapter.StateUpgrading)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("downloads.ActiveSourceCounts: query active chapters: %w", err)
	}

	_, seriesIDs := distinctSeries(rows)
	provByID, provBySeries, err := s.loadProviders(ctx, seriesIDs)
	if err != nil {
		return nil, err
	}
	idxBySeries := make(map[uuid.UUID]upgradeTargetIndex, len(provBySeries))
	for sid, provs := range provBySeries {
		idxBySeries[sid] = newUpgradeTargetIndex(provs)
	}

	counts := map[string]int{}
	for _, ch := range rows {
		if sp := activeFetchSource(ch, provByID, idxBySeries[ch.SeriesID]); sp != nil {
			counts[breakerKey(sp)]++
		}
	}
	return counts, nil
}

// activeFetchSource resolves the source a downloading/upgrading chapter is being
// fetched FROM: the upgrade target for an upgrading chapter (the chapter's CBZ is
// already satisfied — the fetch is aimed at the higher source it is converging to),
// else the top live candidate for a downloading chapter (chapterSource's primary-
// source rule). Returns nil when no source carries the chapter (nothing is fetching
// it), so it takes no slot in the counts.
func activeFetchSource(ch *ent.Chapter, provByID map[uuid.UUID]*ent.SeriesProvider, idx upgradeTargetIndex) *ent.SeriesProvider {
	if isUpgrading(ch.State) {
		if c, ok := upgradeTargetCarrier(ch, idx, provByID); ok {
			return c.provider
		}
		return nil
	}
	sp, _ := chapterSource(ch, provByID, idx)
	return sp
}
