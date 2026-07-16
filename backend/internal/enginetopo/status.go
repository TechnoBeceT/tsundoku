package enginetopo

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	entsourcepreference "github.com/technobecet/tsundoku/internal/ent/sourcepreference"
	entsourceseedstate "github.com/technobecet/tsundoku/internal/ent/sourceseedstate"
)

// Status is a read-only snapshot of how much of the engine topology Tsundoku
// has captured into its own durable store, computed ENTIRELY from DB counts —
// it never calls the engine, so it is safe to serve on every request without an
// upstream round-trip. It is the observable counterpart of the one-shot seed
// passes (RunSeed): each field answers "how far did the seed get" for one pass.
//
// An empty database yields the zero Status (every count 0) — a valid, expected
// answer for a fresh install that has adopted nothing yet, never an error.
//
// (QCAT-253, P2 Suwayomi-removal slice 5): URLsFilled/URLsRemaining are RETIRED
// along with the SeriesProvider.url backfill pass they reported on (see
// runner.go's RunSeed doc comment) — sourceengine-backed ingest sets url at
// write time, so there is no longer a backfill gap to measure.
type Status struct {
	// Repos is the number of harvested extension-repository rows (HarvestedRepo).
	Repos int
	// ExtensionsTotal is the number of harvested extensions (HarvestedExtension).
	ExtensionsTotal int
	// ExtensionsCached is how many of those extensions have their .apk bytes
	// cached locally (apk_cached=true) — the difference from ExtensionsTotal is
	// the set that could not be cached (a gap the recovery path cannot fill).
	ExtensionsCached int
	// SourcesTotal is the number of distinct NUMERIC SeriesProvider.provider
	// values — the library's live-source universe (a disk-origin provider stores
	// a display name, not a numeric source id, and is excluded, mirroring exactly
	// what SeedSourcePreferences iterates).
	SourcesTotal int
	// SourcesPrefsCaptured is the number of distinct sources that have at least
	// one captured SourcePreference row — of SourcesTotal, how many the
	// preference seed has reached.
	SourcesPrefsCaptured int
	// SourcesReached is the number of sources whose last preference-READ SUCCEEDED
	// (SourceSeedState.prefs_read_ok=true) — a source may be reached yet have zero
	// non-default preferences, so this is DISTINCT from SourcesPrefsCaptured.
	SourcesReached int
	// SourcesFailed is the number of sources whose last preference-READ ERRORED
	// (SourceSeedState.prefs_read_ok=false) — a real gap the status reports
	// positively rather than inferring it from a missing count.
	SourcesFailed int
	// FailedSources names the sources (source_name, falling back to the source_id
	// string when the name is "") whose last preference-READ errored, sorted for
	// deterministic output. Always non-nil (an empty slice when none failed).
	FailedSources []string
}

// TopologyStatus computes the engine-topology Status from DB counts alone (no
// engine calls). Every count is a bounded aggregate query; the distinct-set
// counts (numeric providers, sources-with-prefs, seed outcomes) load only the
// single id column they count. err is returned only when a count query itself
// fails — an empty database is the zero Status with a nil error.
func TopologyStatus(ctx context.Context, db *ent.Client) (Status, error) {
	var s Status
	var err error

	if s.Repos, err = db.HarvestedRepo.Query().Count(ctx); err != nil {
		return Status{}, fmt.Errorf("enginetopo.TopologyStatus: count repos: %w", err)
	}
	if s.ExtensionsTotal, err = db.HarvestedExtension.Query().Count(ctx); err != nil {
		return Status{}, fmt.Errorf("enginetopo.TopologyStatus: count extensions: %w", err)
	}
	if s.ExtensionsCached, err = db.HarvestedExtension.Query().
		Where(entharvestedextension.ApkCached(true)).Count(ctx); err != nil {
		return Status{}, fmt.Errorf("enginetopo.TopologyStatus: count cached extensions: %w", err)
	}
	// Load the live-source universe ONCE and share it across all three
	// source-scoped counts (total, prefs-captured, seed-outcomes), so a count can
	// never exceed SourcesTotal — a removed provider's lingering SourcePreference
	// or SourceSeedState rows are uniformly excluded.
	liveIDs, err := liveNumericSources(ctx, db)
	if err != nil {
		return Status{}, err
	}
	s.SourcesTotal = len(liveIDs)
	if s.SourcesPrefsCaptured, err = countSourcesWithPrefs(ctx, db, liveIDs); err != nil {
		return Status{}, err
	}
	if err = computeSeedOutcomes(ctx, db, liveIDs, &s); err != nil {
		return Status{}, err
	}

	return s, nil
}

// liveNumericSources returns the set of distinct SeriesProvider.provider values
// that parse as a numeric engine source id (a live-ingested row), skipping the
// display-name providers a disk-origin row carries — the same numeric/name split
// SeedSourcePreferences applies. SourcesTotal is len(set), and the set doubles as
// the LIVE-source filter for the seed-outcome counts so a SourceSeedState row for
// a removed provider is never counted (reached+failed can never exceed total).
func liveNumericSources(ctx context.Context, db *ent.Client) (map[int64]bool, error) {
	providers, err := db.SeriesProvider.Query().
		Unique(true).
		Select(entseriesprovider.FieldProvider).
		Strings(ctx)
	if err != nil {
		return nil, fmt.Errorf("enginetopo.TopologyStatus: query providers: %w", err)
	}
	ids := make(map[int64]bool, len(providers))
	for _, p := range providers {
		if id, perr := strconv.ParseInt(p, 10, 64); perr == nil {
			ids[id] = true
		}
	}
	return ids, nil
}

// countSourcesWithPrefs counts the distinct source ids that carry at least one
// SourcePreference row AND are still a LIVE source (in live) — scoped to the same
// universe as SourcesTotal, so a removed source whose SourcePreference rows linger
// (they are not cleaned on RemoveProvider/DeleteSeries) never pushes
// prefsCaptured above total. Only the source_id column is selected — never the
// .Sensitive() value column.
func countSourcesWithPrefs(ctx context.Context, db *ent.Client, live map[int64]bool) (int, error) {
	ids, err := db.SourcePreference.Query().
		Unique(true).
		Select(entsourcepreference.FieldSourceID).
		Ints(ctx)
	if err != nil {
		return 0, fmt.Errorf("enginetopo.TopologyStatus: query source-pref sources: %w", err)
	}
	count := 0
	for _, id := range ids {
		if live[int64(id)] {
			count++
		}
	}
	return count, nil
}

// computeSeedOutcomes fills SourcesReached / SourcesFailed / FailedSources on s,
// scoped to LIVE sources only: a SourceSeedState row whose source_id is no longer
// a current numeric SeriesProvider.provider (its provider was removed by
// RemoveProvider/DeleteSeries, which do not touch this bookkeeping table) is
// IGNORED — so reached+failed never exceed SourcesTotal and a removed source
// drops out of FailedSources immediately. One All() over the seed-state rows,
// intersected in memory with the live-id set (no N+1). Only the columns needed
// are selected (SourceSeedState has no .Sensitive() column). FailedSources is
// sorted (name, falling back to the source_id string) and always non-nil.
func computeSeedOutcomes(ctx context.Context, db *ent.Client, live map[int64]bool, s *Status) error {
	rows, err := db.SourceSeedState.Query().
		Select(
			entsourceseedstate.FieldSourceID,
			entsourceseedstate.FieldSourceName,
			entsourceseedstate.FieldPrefsReadOk,
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("enginetopo.TopologyStatus: query seed states: %w", err)
	}
	failed := make([]string, 0, len(rows))
	for _, r := range rows {
		if !live[r.SourceID] {
			// Stale row for a source whose provider was removed — not in the live
			// universe, so it must not inflate the counts nor haunt FailedSources.
			continue
		}
		if r.PrefsReadOk {
			s.SourcesReached++
			continue
		}
		s.SourcesFailed++
		name := r.SourceName
		if name == "" {
			name = strconv.FormatInt(r.SourceID, 10)
		}
		failed = append(failed, name)
	}
	sort.Strings(failed)
	s.FailedSources = failed
	return nil
}
