package library

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// CollapseIgnoredScanlatorSource is the Slice-B on-enable migration for the
// per-source "ignore scanlator" flag (QCAT-287). Slice A made flagging a source
// apply-forward only: new adopts collapse to a single [Source] provider, but an
// ALREADY-adopted series keeps its per-uploader SeriesProvider rows
// ([Hive Scans-Admin], [Hive Scans-Aero], …) and its per-uploader CBZ filenames.
// This method is the missing migration: it sweeps EVERY series that carries the
// flagged source and folds that source's per-uploader rows into ONE
// scanlator="" provider, relabelling the affected CBZs [Source-Uploader] →
// [Source] on disk.
//
// It is the SOURCE-WIDE sibling of DedupAllProviders, and reuses the SAME
// no-redownload merge engine (mergeDiskIntoLive → relabelOverlap → commitMatch →
// deleteDrainedDiskProvider). The only thing that differs is the GROUPING: dedup
// folds a disk/live twin that share a display NAME + scanlator; this folds rows
// that share the same numeric source id but DIFFER in scanlator.
//
// Resilience mirrors DedupAllProviders exactly: a per-series failure is logged
// and skipped (one bad series never aborts the sweep); a series that vanished
// mid-sweep is silently ignored. It returns how many series were collapsed
// (seriesProcessed), how many per-uploader rows were folded in total (merged),
// and how many series were skipped after an error (skipped).
//
// ONE-WAY: this is only ever invoked when the flag is turned ON. Un-flagging a
// source does NOT un-merge — the collapsed [Source] provider and its relabeled
// CBZs are kept (re-splitting would require re-downloading each uploader's
// chapters, which the never-auto-delete invariant forbids). See the handler
// (SetSourceIgnoreScanlator) for where this is wired.
//
// IDEMPOTENT: a source with no per-uploader rows left (already collapsed, or
// never split) is a no-op, so a re-run — or a partial run that failed on some
// series — can safely be repeated to completion.
func (s *Service) CollapseIgnoredScanlatorSource(ctx context.Context, sourceID int64) (seriesProcessed, merged, skipped int, err error) {
	provider := strconv.FormatInt(sourceID, 10)

	seriesIDs, err := s.seriesWithSource(ctx, provider)
	if err != nil {
		return 0, 0, 0, err
	}

	for _, sid := range seriesIDs {
		if ctx.Err() != nil {
			return seriesProcessed, merged, skipped, ctx.Err()
		}
		folded, cErr := s.collapseSourceInSeries(ctx, sid, provider)
		if cErr != nil {
			slog.WarnContext(ctx, "library.CollapseIgnoredScanlatorSource: series collapse failed, skipping",
				"series_id", sid, "source_id", sourceID, "err", cErr)
			skipped++
			continue
		}
		if folded > 0 {
			seriesProcessed++
			merged += folded
		}
	}

	// One convergence trigger for the whole sweep (mirrors DedupProviders, which
	// triggers per merged series — collapsing many series at once, a single
	// trigger is enough to pick up any re-pointed chapters).
	if merged > 0 && s.trigger != nil {
		s.trigger()
	}
	return seriesProcessed, merged, skipped, nil
}

// seriesWithSource returns the distinct series ids that carry a SeriesProvider
// for the given numeric provider (source id string) — the exact set the collapse
// sweep must visit. Reads only the series_id column (no full-row load) and
// de-duplicates in memory (a series with multiple per-uploader rows appears once).
func (s *Service) seriesWithSource(ctx context.Context, provider string) ([]uuid.UUID, error) {
	rows, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.Provider(provider)).
		Select(entseriesprovider.FieldSeriesID).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("library.CollapseIgnoredScanlatorSource: list series for source %q: %w", provider, err)
	}
	seen := make(map[uuid.UUID]bool, len(rows))
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if seen[r.SeriesID] {
			continue
		}
		seen[r.SeriesID] = true
		out = append(out, r.SeriesID)
	}
	return out, nil
}

// collapseSourceInSeries folds every per-uploader SeriesProvider of one source
// in one series into a single scanlator="" survivor, relabelling the affected
// CBZs [Source-Uploader] → [Source]. It returns how many per-uploader rows were
// folded (0 = already collapsed / nothing to do).
//
// Design (reuses the battle-tested merge engine, one fold per uploader):
//  1. Load the series (WithCategory — relabelOverlap needs the on-disk folder
//     path) and every SeriesProvider row for this source. Partition into the
//     scanlator="" survivor (if one already exists) and the per-uploader rows.
//     No uploader rows ⇒ nothing to collapse (idempotent no-op).
//  2. Ensure a survivor exists: reuse an existing "" row, else CREATE a fresh
//     one that copies the source's identity (provider id, display name, language,
//     title, cover, url) from the highest-importance uploader, PARKED at
//     importance 0.
//  3. Merge every uploader's ProviderChapter feed into the survivor (union,
//     de-duplicated by chapter_key) so the survivor offers every collapsed
//     chapter's key — relabelOverlap keys its relabel off the SURVIVOR's feed,
//     so this is what makes the drained rows' chapters actually get relabeled
//     (and re-pointed) rather than orphaned.
//  4. Fold each uploader into the survivor via mergeDiskIntoLive at target
//     importance 0. Keeping the survivor at 0 for the WHOLE window is deliberate:
//     it is the merge engine's park sentinel (0 <= any watermark), so no
//     background upgrade can re-download onto a filename mid-relabel. Each fold
//     is atomic + rollback-guarded by mergeDiskIntoLive (a failed fold leaves
//     that fold's series state byte-for-byte unchanged).
//  5. Finally elevate the survivor AND every chapter it now satisfies to the
//     real target importance in one transaction, so no committed state ever has
//     the survivor out-ranking the chapters it satisfies.
//
// COLLISION SAFETY (the highest-risk edge, reasoned about explicitly): "two
// uploaders both have a CBZ for the same chapter number" cannot happen here. A
// numbered chapter's chapter_key IS its canonical number
// (chapter.NormalizeChapterKey), UNIQUE(series_id, chapter_key) means exactly one
// Chapter row per number per series, and a Chapter has exactly one satisfied_by —
// so at most one uploader satisfies (and owns a CBZ for) any given number. The
// relabeled filename's disambiguator (disk.buildChapterStr) is ALSO derived from
// the chapter_key, so two DISTINCT collapsed chapters always relabel to DISTINCT
// filenames. The relabel therefore only strips the "-Uploader" suffix; it never
// needs a winner/loser decision and never overwrites another chapter's file. The
// only rows deleted are the drained per-uploader SeriesProvider rows + their
// (now-merged) ProviderChapter feeds (sanctioned, exactly like the existing merge
// path); CBZs are RELABELED, never deleted — never-auto-delete holds.
func (s *Service) collapseSourceInSeries(ctx context.Context, seriesID uuid.UUID, provider string) (int, error) {
	row, err := s.loadSeriesForCollapse(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	if row == nil {
		// Deleted mid-sweep — benign, nothing to collapse.
		return 0, nil
	}

	all, err := s.db.SeriesProvider.Query().
		Where(entseriesprovider.SeriesID(seriesID), entseriesprovider.Provider(provider)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("library.CollapseIgnoredScanlatorSource: load source providers for series %s: %w", seriesID, err)
	}

	survivor, uploaders, target := partitionSourceProviders(all)
	if len(uploaders) == 0 {
		// Already collapsed (only a "" row, or nothing) — idempotent no-op.
		return 0, nil
	}

	survivor, createdSurvivor, err := s.ensureSurvivorWithFeed(ctx, seriesID, survivor, uploaders)
	if err != nil {
		return 0, err
	}

	folded, err := s.foldUploaders(ctx, row, survivor, uploaders, createdSurvivor)
	if err != nil {
		return folded, err
	}

	if err := s.finalizeCollapsedSurvivor(ctx, survivor.ID, target); err != nil {
		return folded, err
	}
	return folded, nil
}

// loadSeriesForCollapse loads the series (with its Category edge, needed for the
// on-disk relabel path). A series deleted mid-sweep yields (nil, nil) so the
// caller treats it as a benign no-op rather than an error.
func (s *Service) loadSeriesForCollapse(ctx context.Context, seriesID uuid.UUID) (*ent.Series, error) {
	row, err := s.db.Series.Query().
		Where(entseries.IDEQ(seriesID)).
		WithCategory().
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("library.CollapseIgnoredScanlatorSource: load series %s: %w", seriesID, err)
	}
	return row, nil
}

// ensureSurvivorWithFeed resolves the collapse survivor — reusing an existing
// scanlator="" row or creating a fresh parked one — and populates its feed with
// the union of the uploaders' feeds. If the feed-merge fails on a freshly-created
// survivor it is cleaned up so the series is left byte-for-byte unchanged.
// Returns the survivor and whether it was created in this call.
func (s *Service) ensureSurvivorWithFeed(ctx context.Context, seriesID uuid.UUID, survivor *ent.SeriesProvider, uploaders []*ent.SeriesProvider) (*ent.SeriesProvider, bool, error) {
	created := false
	if survivor == nil {
		sp, err := s.createCollapsedSurvivor(ctx, seriesID, highestImportance(uploaders))
		if err != nil {
			return nil, false, err
		}
		survivor = sp
		created = true
	}
	if err := s.mergeUploaderFeeds(ctx, survivor, uploaders); err != nil {
		s.cleanupFreshSurvivor(ctx, survivor.ID, created)
		return nil, false, err
	}
	return survivor, created, nil
}

// foldUploaders folds every uploader into the survivor via mergeDiskIntoLive at
// target importance 0 (the survivor stays parked for the whole window — see the
// design note on collapseSourceInSeries). If the FIRST fold fails and the
// survivor was freshly created, it is cleaned up (byte-for-byte rollback); once a
// fold has committed the survivor owns real chapters and is kept (a re-run
// completes the partial collapse). Returns how many uploaders were folded.
func (s *Service) foldUploaders(ctx context.Context, row *ent.Series, survivor *ent.SeriesProvider, uploaders []*ent.SeriesProvider, createdSurvivor bool) (int, error) {
	folded := 0
	for _, up := range uploaders {
		if _, err := s.mergeDiskIntoLive(ctx, row, up, survivor, 0); err != nil {
			if folded == 0 {
				s.cleanupFreshSurvivor(ctx, survivor.ID, createdSurvivor)
			}
			return folded, err
		}
		folded++
	}
	return folded, nil
}

// partitionSourceProviders splits a source's SeriesProvider rows into the
// scanlator="" survivor (the first one found; at most one is expected) and the
// per-uploader rows (scanlator != ""), and returns the highest importance across
// ALL of them (the target the collapsed provider inherits). A pure helper.
func partitionSourceProviders(all []*ent.SeriesProvider) (survivor *ent.SeriesProvider, uploaders []*ent.SeriesProvider, target int) {
	for _, sp := range all {
		if sp.Importance > target {
			target = sp.Importance
		}
		if sp.Scanlator == "" {
			if survivor == nil {
				survivor = sp
			}
			continue
		}
		uploaders = append(uploaders, sp)
	}
	return survivor, uploaders, target
}

// highestImportance returns the highest-importance uploader row (ties keep the
// first) — the template whose source identity (provider id, display name,
// language, title, cover, url) a freshly-created survivor copies. Callers pass a
// non-empty slice (guarded by len(uploaders) == 0 upstream).
func highestImportance(uploaders []*ent.SeriesProvider) *ent.SeriesProvider {
	best := uploaders[0]
	for _, u := range uploaders[1:] {
		if u.Importance > best.Importance {
			best = u
		}
	}
	return best
}

// createCollapsedSurvivor creates the scanlator="" SeriesProvider that every
// per-uploader row folds into. It copies the source's identity from template
// (the highest-importance uploader — they are all the same physical source, so
// any would do) and starts PARKED at importance 0 (its real importance is set by
// finalizeCollapsedSurvivor after the folds). suwayomi_id is copied only when
// non-zero so the row stays a linked/live provider (series.IsLinkedProvider keys
// off the numeric provider field, which is copied verbatim).
func (s *Service) createCollapsedSurvivor(ctx context.Context, seriesID uuid.UUID, template *ent.SeriesProvider) (*ent.SeriesProvider, error) {
	create := s.db.SeriesProvider.Create().
		SetSeriesID(seriesID).
		SetProvider(template.Provider).
		SetProviderName(template.ProviderName).
		SetScanlator("").
		SetLanguage(template.Language).
		SetTitle(template.Title).
		SetCoverURL(template.CoverURL).
		SetURL(template.URL).
		SetWebURL(template.WebURL).
		SetImportance(0)
	if template.SuwayomiID != 0 {
		create.SetSuwayomiID(template.SuwayomiID)
	}
	sp, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("library.CollapseIgnoredScanlatorSource: create collapsed provider for series %s: %w", seriesID, err)
	}
	return sp, nil
}

// mergeUploaderFeeds copies every uploader's ProviderChapter feed into the
// survivor's feed, de-duplicated by chapter_key (the survivor's existing keys win;
// then the first uploader to carry a key wins). This makes the survivor's feed
// the UNION of the collapsed sources' offerings, which relabelOverlap reads to
// decide which chapters to relabel/re-point — so every satisfied chapter gets
// collapsed (a satisfied chapter always has a feed row on its satisfier, so the
// union covers all of them). The per-source retry state (attempts/last_error/
// next_attempt_at) is deliberately NOT copied — the collapsed provider gets a
// fresh retry budget.
func (s *Service) mergeUploaderFeeds(ctx context.Context, survivor *ent.SeriesProvider, uploaders []*ent.SeriesProvider) error {
	existing, err := s.db.ProviderChapter.Query().
		Where(entproviderchapter.SeriesProviderID(survivor.ID)).
		All(ctx)
	if err != nil {
		return fmt.Errorf("library.CollapseIgnoredScanlatorSource: load survivor feed: %w", err)
	}
	seen := make(map[string]bool, len(existing))
	for _, pc := range existing {
		seen[pc.ChapterKey] = true
	}

	for _, up := range uploaders {
		feed, fErr := s.db.ProviderChapter.Query().
			Where(entproviderchapter.SeriesProviderID(up.ID)).
			All(ctx)
		if fErr != nil {
			return fmt.Errorf("library.CollapseIgnoredScanlatorSource: load uploader feed %s: %w", up.ID, fErr)
		}
		for _, pc := range feed {
			if seen[pc.ChapterKey] {
				continue
			}
			if cErr := s.copyProviderChapter(ctx, survivor.ID, pc); cErr != nil {
				return cErr
			}
			seen[pc.ChapterKey] = true
		}
	}
	return nil
}

// copyProviderChapter inserts one feed row onto the survivor, copying the
// availability fields (key/number/name/urls/index/page-count/suwayomi id) but
// NOT the per-source retry state (see mergeUploaderFeeds). Nillable fields are
// only set when present so a copy never invents a zero value.
func (s *Service) copyProviderChapter(ctx context.Context, survivorID uuid.UUID, pc *ent.ProviderChapter) error {
	create := s.db.ProviderChapter.Create().
		SetSeriesProviderID(survivorID).
		SetChapterKey(pc.ChapterKey).
		SetName(pc.Name).
		SetURL(pc.URL).
		SetWebURL(pc.WebURL).
		SetProviderIndex(pc.ProviderIndex)
	if pc.Number != nil {
		create.SetNumber(*pc.Number)
	}
	if pc.ProviderUploadDate != nil {
		create.SetProviderUploadDate(*pc.ProviderUploadDate)
	}
	if pc.PageCount != nil {
		create.SetPageCount(*pc.PageCount)
	}
	if pc.SuwayomiChapterID != 0 {
		create.SetSuwayomiChapterID(pc.SuwayomiChapterID)
	}
	if err := create.Exec(ctx); err != nil {
		return fmt.Errorf("library.CollapseIgnoredScanlatorSource: copy feed row %q onto survivor: %w", pc.ChapterKey, err)
	}
	return nil
}

// finalizeCollapsedSurvivor lifts the survivor out of its parked state: it sets
// the survivor's importance AND every chapter it now satisfies to the target
// importance in ONE transaction, so no committed state ever has the survivor
// out-ranking the chapters it satisfies (importance == satisfied_importance ⇒
// download.DetectUpgrades' strict `>` gate never fires ⇒ no re-download).
func (s *Service) finalizeCollapsedSurvivor(ctx context.Context, survivorID uuid.UUID, target int) error {
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return fmt.Errorf("library.CollapseIgnoredScanlatorSource: begin finalize tx: %w", err)
	}
	if err := tx.SeriesProvider.UpdateOneID(survivorID).SetImportance(target).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("library.CollapseIgnoredScanlatorSource: set collapsed provider importance: %w", err)
	}
	if err := tx.Chapter.Update().
		Where(entchapter.SatisfiedByProviderIDEQ(survivorID)).
		SetSatisfiedImportance(target).
		Exec(ctx); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("library.CollapseIgnoredScanlatorSource: re-point satisfied_importance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("library.CollapseIgnoredScanlatorSource: commit finalize tx: %w", err)
	}
	return nil
}

// cleanupFreshSurvivor deletes a just-created survivor (and its copied feed) when
// a collapse fails BEFORE any uploader fold has committed, so a failed first fold
// leaves the series byte-for-byte unchanged. It is a no-op unless the survivor
// was freshly created in this call. Best-effort: a cleanup error is logged, never
// returned (it must not mask the primary collapse error, and a stray empty
// [Source] provider is reconcile-harmless — a later run reuses it).
func (s *Service) cleanupFreshSurvivor(ctx context.Context, survivorID uuid.UUID, created bool) {
	if !created {
		return
	}
	if _, err := s.db.ProviderChapter.Delete().
		Where(entproviderchapter.SeriesProviderID(survivorID)).
		Exec(ctx); err != nil {
		slog.Error("library.CollapseIgnoredScanlatorSource: cleanup of fresh survivor feed failed (left an empty provider — reconcile-harmless)",
			"provider_id", survivorID, "err", err)
	}
	if err := s.db.SeriesProvider.DeleteOneID(survivorID).Exec(ctx); err != nil {
		slog.Error("library.CollapseIgnoredScanlatorSource: cleanup of fresh survivor row failed (left an empty provider — reconcile-harmless)",
			"provider_id", survivorID, "err", err)
	}
}
