package download

import (
	"context"
	"fmt"
	"log/slog"
	"math"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
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

	chapters, err := d.client.Chapter.Query().
		Where(entchapter.NumberNotNil()).
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
		if gerr != nil {
			slog.WarnContext(ctx, "download.DetectSupersededParts: series group failed, skipping", "err", gerr)
			continue
		}
		superseded += s
		reverted += r
	}
	return superseded, reverted, nil
}

// isWholeNumber reports whether n has no fractional part (n == trunc(n)).
func isWholeNumber(n float64) bool { return n == math.Trunc(n) }

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
		if isWholeNumber(n) {
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
