package enginetopo

import (
	"context"
	"fmt"
	"strconv"

	"github.com/technobecet/tsundoku/internal/ent"
	entharvestedextension "github.com/technobecet/tsundoku/internal/ent/harvestedextension"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	entsourcepreference "github.com/technobecet/tsundoku/internal/ent/sourcepreference"
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
}

// TopologyStatus computes the engine-topology Status from DB counts alone (no
// engine calls). Every count is a bounded aggregate query; the two distinct-set
// counts (numeric providers, sources-with-prefs) load only the single id column
// they count. err is returned only when a count query itself fails — an empty
// database is the zero Status with a nil error.
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
	if s.SourcesTotal, err = countNumericSources(ctx, db); err != nil {
		return Status{}, err
	}
	if s.SourcesPrefsCaptured, err = countSourcesWithPrefs(ctx, db); err != nil {
		return Status{}, err
	}

	return s, nil
}

// countNumericSources counts the distinct SeriesProvider.provider values that
// parse as a numeric engine source id (a live-ingested row), skipping the
// display-name providers a disk-origin row carries — the same numeric/name split
// SeedSourcePreferences applies, so SourcesTotal matches the seed's own source
// universe.
func countNumericSources(ctx context.Context, db *ent.Client) (int, error) {
	providers, err := db.SeriesProvider.Query().
		Unique(true).
		Select(entseriesprovider.FieldProvider).
		Strings(ctx)
	if err != nil {
		return 0, fmt.Errorf("enginetopo.TopologyStatus: query providers: %w", err)
	}
	count := 0
	for _, p := range providers {
		if _, perr := strconv.ParseInt(p, 10, 64); perr == nil {
			count++
		}
	}
	return count, nil
}

// countSourcesWithPrefs counts the distinct source ids that carry at least one
// SourcePreference row (a source the preference seed has reached). Only the
// source_id column is selected — never the .Sensitive() value column.
func countSourcesWithPrefs(ctx context.Context, db *ent.Client) (int, error) {
	ids, err := db.SourcePreference.Query().
		Unique(true).
		Select(entsourcepreference.FieldSourceID).
		Ints(ctx)
	if err != nil {
		return 0, fmt.Errorf("enginetopo.TopologyStatus: count source-pref sources: %w", err)
	}
	return len(ids), nil
}
