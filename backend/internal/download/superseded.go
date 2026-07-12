package download

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
)

// DetectSupersededParts implements fractional-part suppression: when a whole
// integer chapter N is downloaded and there are >=2 fractional "parts" under it
// (any chapter whose number truncates to N), each part currently wanted or
// downloaded is transitioned to superseded (its CBZ, if any, is best-effort
// removed and its filename cleared). A lone side-chapter (only 1 fractional
// chapter under N) is never suppressed. It also reverts: a superseded part whose
// whole is no longer downloaded goes back to wanted. When the
// jobs.suppress_split_parts setting is disabled, every superseded part reverts to
// wanted and nothing else happens. Idempotent — a re-run with unchanged state
// supersedes/reverts nothing new. Never touches downloading/upgrading/failed/
// already-superseded parts for the SUPERSEDE action (the >=2 count still counts
// every fractional chapter regardless of state). File removal is best-effort
// (logged, not fatal — the state transition still applies); no Chapter row is
// ever deleted (never-auto-delete).
func (d *Dispatcher) DetectSupersededParts(ctx context.Context) (superseded, reverted int, err error) {
	enabled := d.retry.SuppressSplitParts(ctx)

	// EFFICIENCY (M2): NARROW THE QUERY. A series with no fractional-numbered
	// chapter can never have a superseded/eligible part, so grouping + scanning it
	// is pure waste (supersedeSeriesGroup on a whole-only group does no DB writes —
	// downloadedWholes is populated but partsByWhole is empty, making the
	// supersede/revert loops no-ops — so the real cost was the full-table load).
	// So we first find the series that actually own a fractional chapter, then load
	// numbered chapters ONLY for those series. The first query returns full rows for
	// the (small) fractional subset only; the heavy per-series load is scoped by an
	// IN predicate. Loading fractional rows as full ent entities (rather than a
	// Select+Scan of series_id) keeps uuid handling on the framework's own value
	// scanner instead of a hand-rolled column scan.
	fractionalSeriesIDs, err := d.seriesWithFractionalChapters(ctx)
	if err != nil {
		return 0, 0, err
	}
	if len(fractionalSeriesIDs) == 0 {
		return 0, 0, nil
	}

	chapters, err := d.client.Chapter.Query().
		Where(
			entchapter.NumberNotNil(),
			entchapter.SeriesIDIn(fractionalSeriesIDs...),
		).
		WithSeries(func(sq *ent.SeriesQuery) { sq.WithCategory() }).
		All(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("download.DetectSupersededParts: query chapters: %w", err)
	}

	bySeries := make(map[string][]*ent.Chapter)
	for _, ch := range chapters {
		key := ch.SeriesID.String()
		bySeries[key] = append(bySeries[key], ch)
	}

	for _, group := range bySeries {
		s, r, gerr := d.supersedeSeriesGroup(ctx, group, enabled)
		// M4: add the PARTIAL counts even on error. supersedeSeriesGroup may have
		// already applied some DB transitions before failing mid-group, so the
		// returned totals must reflect DB reality — dropping s/r on a mid-group
		// error would under-report what actually happened.
		superseded += s
		reverted += r
		if gerr != nil {
			slog.WarnContext(ctx, "download.DetectSupersededParts: series group failed, skipping", "err", gerr)
			continue
		}
	}
	return superseded, reverted, nil
}

// seriesWithFractionalChapters returns the distinct series ids that own at least
// one fractional-numbered chapter (a chapter whose number has a non-zero
// fractional part). Ent has no built-in "is fractional" predicate, so the filter
// drops to a custom SQL predicate (number <> trunc(number)); Postgres trunc() is
// exact on the NUMERIC number column and NULLs are already excluded by
// NumberNotNil. The fractional subset is small, so loading it as full ent rows and
// de-duplicating series ids in memory is cheap and avoids a hand-rolled uuid
// column scan.
func (d *Dispatcher) seriesWithFractionalChapters(ctx context.Context) ([]uuid.UUID, error) {
	fractional, err := d.client.Chapter.Query().
		Where(
			entchapter.NumberNotNil(),
			func(s *sql.Selector) {
				col := s.C(entchapter.FieldNumber)
				s.Where(sql.ExprP(fmt.Sprintf("%s <> trunc(%s)", col, col)))
			},
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("download.DetectSupersededParts: query fractional chapters: %w", err)
	}

	seen := make(map[uuid.UUID]struct{}, len(fractional))
	ids := make([]uuid.UUID, 0, len(fractional))
	for _, ch := range fractional {
		if _, ok := seen[ch.SeriesID]; ok {
			continue
		}
		seen[ch.SeriesID] = struct{}{}
		ids = append(ids, ch.SeriesID)
	}
	return ids, nil
}

// wholeOf returns the integer "whole" a fractional part number belongs under.
func wholeOf(n float64) float64 { return math.Trunc(n) }

// supersedeSeriesGroup applies fractional-part suppression to one series' worth
// of numbered chapters (already loaded WithSeries(WithCategory())).
func (d *Dispatcher) supersedeSeriesGroup(ctx context.Context, group []*ent.Chapter, enabled bool) (superseded, reverted int, err error) {
	downloadedWholes := map[float64]bool{}
	partsByWhole := map[float64][]*ent.Chapter{}
	var supersededParts []*ent.Chapter

	for _, ch := range group {
		if ch.Number == nil {
			continue
		}
		n := *ch.Number
		if !chapterrange.IsFractional(n) {
			if ch.State == entchapter.StateDownloaded {
				downloadedWholes[n] = true
			}
			continue
		}
		partsByWhole[wholeOf(n)] = append(partsByWhole[wholeOf(n)], ch)
		if ch.State == entchapter.StateSuperseded {
			supersededParts = append(supersededParts, ch)
		}
	}

	if !enabled {
		reverted, err = d.revertAll(ctx, supersededParts)
		return 0, reverted, err
	}

	reverted, err = d.revertOrphaned(ctx, supersededParts, downloadedWholes)
	if err != nil {
		return 0, reverted, err
	}
	superseded, err = d.supersedeEligible(ctx, partsByWhole, downloadedWholes)
	return superseded, reverted, err
}

// revertAll returns every given superseded part to wanted (suppression
// disabled — restore everything).
func (d *Dispatcher) revertAll(ctx context.Context, parts []*ent.Chapter) (int, error) {
	reverted := 0
	for _, p := range parts {
		if err := chapter.SetState(ctx, d.client, p.ID, entchapter.StateWanted); err != nil {
			return reverted, err
		}
		reverted++
	}
	return reverted, nil
}

// revertOrphaned reverts superseded parts whose whole is no longer downloaded
// back to wanted.
//
// The "whole is gone" test keys off DB TRUTH — whether the whole's Chapter row is
// in StateDownloaded (downloadedWholes) — NOT disk presence. A downloaded-but-
// missing-on-disk whole is deliberately left as StateDownloaded by disk.Reconcile
// (see reconcileChapters), so a part superseded under it does NOT auto-revert on a
// transient scan fault (e.g. an NFS blip). This is owner-ratified: auto-downgrading
// a whole on a scan glitch would trigger needless re-downloads; recovery of a
// genuinely-lost whole is a manual owner retry.
func (d *Dispatcher) revertOrphaned(ctx context.Context, parts []*ent.Chapter, downloadedWholes map[float64]bool) (int, error) {
	reverted := 0
	for _, p := range parts {
		if downloadedWholes[wholeOf(*p.Number)] {
			continue
		}
		if err := chapter.SetState(ctx, d.client, p.ID, entchapter.StateWanted); err != nil {
			return reverted, err
		}
		reverted++
	}
	return reverted, nil
}

// supersedeEligible supersedes every wanted/downloaded part under a downloaded
// whole that has >=2 fractional parts (counting ALL parts regardless of state).
//
// The len(parts) < 2 gate is a split-parts heuristic: a genuine side-story is
// almost always a single .5, whereas >=2 fractional parts under a downloaded whole
// N is the split-chapter signature (N.1, N.2, … that together ARE N). Owner-
// ratified known edge: if a series genuinely has >=2 DISTINCT side-stories under a
// downloaded whole (e.g. a real N.1 AND N.2 that are NOT split-parts of N), they
// are superseded and their CBZs best-effort removed. This is accepted and fully
// reversible — supersede never deletes a Chapter row, so recovery is a manual
// re-download (the part reverts to wanted, then downloads again).
func (d *Dispatcher) supersedeEligible(ctx context.Context, partsByWhole map[float64][]*ent.Chapter, downloadedWholes map[float64]bool) (int, error) {
	superseded := 0
	for whole, parts := range partsByWhole {
		if !downloadedWholes[whole] || len(parts) < 2 {
			continue
		}
		for _, p := range parts {
			if p.State != entchapter.StateWanted && p.State != entchapter.StateDownloaded {
				continue
			}
			if err := d.supersedeOnePart(ctx, p); err != nil {
				return superseded, err
			}
			superseded++
		}
	}
	return superseded, nil
}

// supersedeOnePart transitions a part to superseded, best-effort removes its CBZ
// (if any), and clears its filename. Removal failure is logged, not fatal — the
// state transition already applied and no Chapter row is ever deleted.
func (d *Dispatcher) supersedeOnePart(ctx context.Context, p *ent.Chapter) error {
	if err := chapter.SetState(ctx, d.client, p.ID, entchapter.StateSuperseded); err != nil {
		return err
	}
	if p.Filename == "" {
		return nil
	}

	title := ""
	if p.Edges.Series != nil {
		title = p.Edges.Series.Title
	}
	if _, rmErr := disk.RemoveChapterFile(d.cfg.Storage, seriesCategoryName(p), title, p.Filename); rmErr != nil {
		slog.WarnContext(ctx, "download.DetectSupersededParts: best-effort part-CBZ removal failed",
			"chapter_id", p.ID, "filename", p.Filename, "err", rmErr)
	}
	if err := d.client.Chapter.UpdateOneID(p.ID).SetFilename("").Exec(ctx); err != nil {
		return fmt.Errorf("download.DetectSupersededParts: clear filename for %s: %w", p.ID, err)
	}
	return nil
}
