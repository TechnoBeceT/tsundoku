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
	// URLsFilled is the number of SeriesProvider rows whose url is populated.
	URLsFilled int
	// URLsRemaining is the number of live (suwayomi_id != 0) SeriesProvider rows
	// still missing a url — exactly BackfillProviderURLs's candidate set, so it
	// is the count that pass can still fill (a disk-origin row with no source is
	// unfillable and deliberately NOT counted here).
	URLsRemaining int
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
	if s.SourcesReached, err = db.SourceSeedState.Query().
		Where(entsourceseedstate.PrefsReadOk(true)).Count(ctx); err != nil {
		return Status{}, fmt.Errorf("enginetopo.TopologyStatus: count reached sources: %w", err)
	}
	if s.SourcesFailed, s.FailedSources, err = failedSources(ctx, db); err != nil {
		return Status{}, err
	}
	if s.URLsFilled, err = db.SeriesProvider.Query().
		Where(entseriesprovider.URLNEQ("")).Count(ctx); err != nil {
		return Status{}, fmt.Errorf("enginetopo.TopologyStatus: count filled urls: %w", err)
	}
	if s.URLsRemaining, err = db.SeriesProvider.Query().
		Where(entseriesprovider.URL(""), entseriesprovider.SuwayomiIDNEQ(0)).Count(ctx); err != nil {
		return Status{}, fmt.Errorf("enginetopo.TopologyStatus: count remaining urls: %w", err)
	}

	return s, nil
}

// countNumericSources counts the distinct SeriesProvider.provider values that
// parse as a numeric Suwayomi source id (a live-ingested row), skipping the
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
		return 0, fmt.Errorf("enginetopo.TopologyStatus: query source-pref sources: %w", err)
	}
	return len(ids), nil
}

// failedSources returns how many sources' last preference-READ errored and the
// sorted list of their names (source_name, falling back to the source_id string
// when the name is ""). Only the two columns it needs are selected. The slice is
// always non-nil so the caller (and the DTO) serializes it as [] never null.
func failedSources(ctx context.Context, db *ent.Client) (int, []string, error) {
	rows, err := db.SourceSeedState.Query().
		Where(entsourceseedstate.PrefsReadOk(false)).
		Select(entsourceseedstate.FieldSourceID, entsourceseedstate.FieldSourceName).
		All(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("enginetopo.TopologyStatus: query failed sources: %w", err)
	}
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		name := r.SourceName
		if name == "" {
			name = strconv.FormatInt(r.SourceID, 10)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return len(rows), names, nil
}
