package download

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// stillDownloadingStates is the set of NON-terminal, still-actionable chapter
// states whose page-staging dir must be KEPT: a resume (wanted/failed after a
// cooldown, or downloading/upgrading if a crash left one un-reset) legitimately
// re-uses the partially-staged pages. Every OTHER state — downloaded / superseded
// / ignored / permanently_failed — is terminal (or its bytes are already in the
// CBZ), so its staging dir is dead weight the GC reclaims. downloading/upgrading
// are included for robustness even though the boot orphan-reset
// (chapter.ResetOrphanedChapters) runs first and moves them to wanted/downloaded.
var stillDownloadingStates = []entchapter.State{
	entchapter.StateWanted,
	entchapter.StateFailed,
	entchapter.StateDownloading,
	entchapter.StateUpgradeAvailable,
	entchapter.StateUpgrading,
}

// GCStagingRoot removes leaked page-staging dirs under stagingRoot at startup. A
// staging dir is named for its ProviderChapter id (<stagingRoot>/<pcID>/); it is
// KEPT only when that ProviderChapter's chapter is currently in a still-downloading
// state (stillDownloadingStates) — so dirs for completed, permanently-failed, or
// deleted (provider/series removed) chapters are reclaimed. It returns the number
// of dirs removed.
//
// This is the startup BACKSTOP for the leaks the in-flight cleanups miss: a
// chapter-specific permanently_failed reached across restarts, a ProviderChapter
// dropped by series.RemoveProvider, a whole series removed by series.DeleteSeries,
// or a crash before the terminal cleanup ran. Run it ONCE at boot, AFTER
// chapter.ResetOrphanedChapters (so a legitimately in-progress chapter is already
// back in `wanted` and its dir is kept). It is best-effort + NFS-safe: an absent
// staging root is not an error (nothing has staged yet), and a per-dir remove
// failure is collected but never aborts the sweep.
func GCStagingRoot(ctx context.Context, client *ent.Client, stagingRoot string) (removed int, err error) {
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("download.GCStagingRoot: read staging root %s: %w", stagingRoot, err)
	}

	keep, err := keepStagingIDs(ctx, client)
	if err != nil {
		return 0, err
	}

	var firstErr error
	for _, e := range entries {
		if _, ok := keep[e.Name()]; ok {
			continue
		}
		if rmErr := os.RemoveAll(filepath.Join(stagingRoot, e.Name())); rmErr != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("download.GCStagingRoot: remove %s: %w", e.Name(), rmErr)
			}
			continue
		}
		removed++
	}
	return removed, firstErr
}

// keepStagingIDs returns the set of ProviderChapter id strings whose chapter is
// currently still-downloading — the staging dirs GCStagingRoot must NOT remove.
//
// It resolves the join in two bounded queries: (1) the still-downloading chapters
// (a small backlog, keyed by series_id + chapter_key), then (2) the ProviderChapter
// rows whose series+key match, filtered in-memory to the EXACT (series_id,
// chapter_key) pairs so a same-key chapter in a different active series can never
// falsely keep an unrelated provider's dir.
func keepStagingIDs(ctx context.Context, client *ent.Client) (map[string]struct{}, error) {
	chapters, err := client.Chapter.Query().
		Where(entchapter.StateIn(stillDownloadingStates...)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("download.GCStagingRoot: query still-downloading chapters: %w", err)
	}

	activePairs := make(map[string]struct{}, len(chapters))
	seriesIDSet := make(map[uuid.UUID]struct{}, len(chapters))
	keySet := make(map[string]struct{}, len(chapters))
	for _, ch := range chapters {
		activePairs[pairKey(ch.SeriesID, ch.ChapterKey)] = struct{}{}
		seriesIDSet[ch.SeriesID] = struct{}{}
		keySet[ch.ChapterKey] = struct{}{}
	}
	if len(activePairs) == 0 {
		return map[string]struct{}{}, nil
	}

	pcs, err := client.ProviderChapter.Query().
		Where(
			entproviderchapter.ChapterKeyIn(distinctKeys(keySet)...),
			entproviderchapter.HasSeriesProviderWith(entseriesprovider.SeriesIDIn(distinctSeriesIDs(seriesIDSet)...)),
		).
		WithSeriesProvider().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("download.GCStagingRoot: query provider chapters: %w", err)
	}

	keep := make(map[string]struct{}, len(pcs))
	for _, pc := range pcs {
		sp := pc.Edges.SeriesProvider
		if sp == nil {
			continue
		}
		if _, ok := activePairs[pairKey(sp.SeriesID, pc.ChapterKey)]; ok {
			keep[pc.ID.String()] = struct{}{}
		}
	}
	return keep, nil
}

// pairKey builds the composite (series_id, chapter_key) identity a Chapter and a
// ProviderChapter are joined on. The NUL separator can never appear in a UUID or a
// chapter key, so distinct pairs never collide.
func pairKey(seriesID uuid.UUID, chapterKey string) string {
	return seriesID.String() + "\x00" + chapterKey
}

// distinctKeys returns the set's chapter keys as a slice (for an ent In predicate).
func distinctKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// distinctSeriesIDs returns the set's series ids as a slice (for an ent In predicate).
func distinctSeriesIDs(m map[uuid.UUID]struct{}) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	return out
}
