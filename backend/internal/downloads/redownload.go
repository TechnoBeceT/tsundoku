package downloads

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/download"
	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	"github.com/technobecet/tsundoku/internal/ent/predicate"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
)

// ErrNotRedownloadable is returned by RedownloadChapter when the chapter is not in
// the downloaded state, so there is no stored CBZ to replace. The HTTP handler maps
// it to a 409. It is deliberately SEPARATE from ErrNotRetryable: a retry and a
// re-download admit disjoint sets of chapters.
var ErrNotRedownloadable = errors.New("chapter is not downloaded")

// ErrInvalidRedownloadFilter is returned by the bulk re-download when its filter is
// not fully specified. Both fields are mandatory and fail closed on purpose: an
// empty source or a zero cutoff would silently widen a re-queue that costs real
// source calls. The HTTP handler maps it to a 400.
var ErrInvalidRedownloadFilter = errors.New("re-download filter needs a source and a cutoff")

// RedownloadFilter selects the chapters a bulk re-download re-queues.
//
// Source is the CANONICAL source name — the same key the engine source strip and
// the circuit breaker use (see breakerKey): a live-ingested provider's
// provider_name, or a disk-reconciled provider's provider (which stores the display
// name there and leaves provider_name empty). Required.
//
// Scanlator narrows to ONE provider within that source. A provider is a
// (source, scanlator) pair, so this is what lets the owner target a single
// scanlator's output rather than everything the source ever supplied. nil means
// every scanlator of the source; a non-nil value matches exactly, and the empty
// string therefore addresses the source's all-scanlators provider specifically.
//
// Since is the "written at or after" cutoff, matched against Chapter.download_date.
// Required.
//
// 🔴 download_date is the ONLY correct key here and the choice is load-bearing
// (QCAT-345). It records the last time the CBZ was WRITTEN, so a convergence
// upgrade that rewrote an old chapter updates it. first_downloaded_at records only
// the FIRST arrival and is never rewritten — on the live library it reported 59
// chapters where the true set was thousands, a 40x undercount that would have left
// most of the target set untouched while reporting success. Never select on it.
//
// A downloaded chapter carrying NO download_date at all is not matched: "written
// since X" cannot be answered for it, and guessing would re-queue rows the filter
// was never asked about.
type RedownloadFilter struct {
	// Source is the canonical source name (required).
	Source string
	// Scanlator narrows to one (source, scanlator) provider; nil = every scanlator.
	Scanlator *string
	// Since is the download_date cutoff, inclusive (required).
	Since time.Time
}

// validate fails the filter closed when either mandatory field is missing.
func (f RedownloadFilter) validate() error {
	if strings.TrimSpace(f.Source) == "" || f.Since.IsZero() {
		return ErrInvalidRedownloadFilter
	}
	return nil
}

// RedownloadChapter re-queues ONE already-downloaded chapter so the engine fetches
// it again, and is the primitive the bulk sweep applies to a filter.
//
// It is a NEW state edge (downloaded → wanted) and differs in kind from a retry: a
// retry gives a chapter with NO file another go, whereas a re-download deliberately
// replaces a CBZ that already exists — the remedy when the stored bytes are wrong
// but every state field says the chapter is fine. downloads.retryableStates is
// therefore untouched, and RetryChapter still refuses a downloaded chapter.
//
// 🔴 The existing CBZ is KEPT (owner-ratified, QCAT-343). Chapter.filename is left
// set and no file is removed, so a FAILED re-download leaves the old file intact
// (wrong, but readable) rather than nothing. This is why re-download adds NO new
// Rule 2 deletion path.
//
// WHERE the replacement lands depends on which source wins the re-fetch, and the
// reset below deliberately re-opens that question — it hands a fresh budget to
// EVERY source offering the chapter, and chapter.RankedLiveCandidates ranks purely
// by importance with no preference for the provider that satisfied the chapter
// last time. Two outcomes, both safe:
//   - the SAME source wins — the common case — so disk.GenerateCBZFilename renders
//     the same filename and the fresh CBZ overwrites the old one IN PLACE.
//   - a DIFFERENT source wins (typically a higher-importance one that had exhausted
//     its budget and is live again now the reset cleared it). The filename encodes
//     the provider label, so the new CBZ is written alongside the old one and
//     finishDownload points Chapter.filename at it. The old file is KEPT — nothing
//     deletes it, which is Rule 2 holding rather than failing — and the owner's
//     "Remove duplicate files" action (series.Service.DedupeFiles) is what clears
//     the orphan.
//
// The chapter-side reset routes through applyChapterRetryReset — the single shared
// definition RetryChapter and RetryAll also use — so re-download and retry can
// never drift apart on what a re-queue clears (§2 DRY). Every source offering the
// chapter also gets a fresh per-source budget, exactly as an owner retry does:
// without it an exhausted source stays out of candidacy and the re-download quietly
// falls through to a worse source, or to none at all.
//
// Chapter + source resets run in one transaction so they can never half-apply.
// Returns ErrChapterNotFound (→404) for an unknown id, or ErrNotRedownloadable
// (→409) when the chapter is not downloaded.
func (s *Service) RedownloadChapter(ctx context.Context, id uuid.UUID) error {
	ch, err := s.client.Chapter.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrChapterNotFound
		}
		return fmt.Errorf("downloads.RedownloadChapter: load chapter %s: %w", id, err)
	}
	if ch.State != entchapter.StateDownloaded {
		return ErrNotRedownloadable
	}

	err = withTx(ctx, s.client, func(tx *ent.Tx) error {
		if _, err := applyChapterRetryReset(tx.Chapter.Update().Where(entchapter.IDEQ(id))).Save(ctx); err != nil {
			return fmt.Errorf("requeue chapter %s: %w", id, err)
		}
		return resetProviderChapters(ctx, tx, map[uuid.UUID][]string{ch.SeriesID: {ch.ChapterKey}})
	})
	if err != nil {
		return fmt.Errorf("downloads.RedownloadChapter: %w", err)
	}
	return nil
}

// RedownloadPreview reports what a bulk re-download WOULD re-queue, and what it
// would cost, without mutating anything. It is the preview half of the owner-
// triggered sweep: the owner sees the count and the cycle estimate before
// confirming.
//
// 🔴 It deliberately does NOT attempt to detect which files are actually damaged
// (QCAT-345). Image scrambling is a PERMUTATION of tiles, so it preserves every
// pixel, the histogram, the dimensions and the edge count — every cheap image
// statistic is permutation-invariant and cannot see it even in principle (three
// detectors were built and all three failed). The filter re-downloads blind, which
// is the honest remedy.
//
// The cycle estimate assumes the re-fetch is served by the FILTERED source, so it
// spends that source's per-cycle batch. A chapter whose re-fetch is won by a
// different, higher-importance source (see RedownloadChapter for how the reset
// re-opens candidacy) spends THAT source's batch instead, and the two drain in
// parallel — so the quote is a ceiling in that case, as it already is a floor
// whenever a source is cooling down. It is an estimate, never a promise.
//
// One COUNT query regardless of how many chapters match.
func (s *Service) RedownloadPreview(ctx context.Context, filter RedownloadFilter) (RedownloadPreviewDTO, error) {
	if err := filter.validate(); err != nil {
		return RedownloadPreviewDTO{}, err
	}
	matched, err := s.client.Chapter.Query().Where(redownloadPredicates(filter)...).Count(ctx)
	if err != nil {
		return RedownloadPreviewDTO{}, fmt.Errorf("downloads.RedownloadPreview: count matching chapters: %w", err)
	}
	perCycle, cycles := s.redownloadCost(ctx, matched)
	return RedownloadPreviewDTO{
		Matched:         matched,
		PerCycle:        perCycle,
		EstimatedCycles: cycles,
	}, nil
}

// RedownloadAll re-queues every chapter the filter matches, applying
// RedownloadChapter's semantics set-wise, and returns how many chapters it
// ACTUALLY re-queued (the rows the update touched, never the size of the
// selection). The matching set is RE-COMPUTED here from the filter rather than
// trusting ids the preview handed out.
//
// 🔴 The matching set is selected OUTSIDE the transaction, so the update carries
// the downloaded-state predicate a second time rather than trusting the ids alone.
// Between the select and the update a download cycle can move a row on — to
// upgrade_available, or to superseded, which is the automatic split-part
// suppression that deletes the CBZ and clears the filename. An ID-keyed update
// with no state predicate would force-write wanted from whatever state the row
// actually holds, bypassing chapter.CanTransition and resurrecting a part the
// suppression pass just parked. applyRetryAll re-filters its own update for the
// same reason. A row that drifted out is simply left alone; its per-source budgets
// are still cleared, which costs nothing and matches what an owner retry of that
// chapter would have done anyway.
//
// The same guarantees hold as for the single-chapter primitive: no CBZ and no row
// is deleted, every filename is left in place, and the chapter-side reset is
// applyChapterRetryReset.
//
// No-N+1 by construction: the statement count is a small CONSTANT independent of
// how many chapters match AND of how many SERIES they span — ONE select of the
// matching set, then ONE bulk per-source reset (resetProviderChapters ORs the
// per-series clauses into a single update) and ONE bulk chapter reset inside a
// single transaction. At the real remediation sizes — thousands of chapters across
// hundreds of series — a loop over either dimension would be unusable.
//
// Throughput is deliberately NOT touched: the re-queued chapters drain at the
// engine's normal per-source batch, which is the anti-ban throttle. A large sweep
// is meant to take many cycles — see RedownloadPreview for the honest estimate.
func (s *Service) RedownloadAll(ctx context.Context, filter RedownloadFilter) (int, error) {
	if err := filter.validate(); err != nil {
		return 0, err
	}
	matched, err := s.client.Chapter.Query().Where(redownloadPredicates(filter)...).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("downloads.RedownloadAll: load matching chapters: %w", err)
	}
	if len(matched) == 0 {
		return 0, nil
	}

	var requeued int
	err = withTx(ctx, s.client, func(tx *ent.Tx) error {
		if err := resetProviderChapters(ctx, tx, groupKeysBySeries(matched)); err != nil {
			return err
		}
		n, err := applyChapterRetryReset(
			tx.Chapter.Update().Where(
				entchapter.IDIn(chapterIDs(matched)...),
				entchapter.StateEQ(entchapter.StateDownloaded),
			),
		).Save(ctx)
		if err != nil {
			return fmt.Errorf("requeue %d chapters: %w", len(matched), err)
		}
		requeued = n
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("downloads.RedownloadAll: %w", err)
	}
	return requeued, nil
}

// redownloadCost translates a matched count into the honest throughput quote: how
// many of ONE source's chapters a download cycle dispatches (the engine's real
// per-source batch, read from the live setting via download.BatchPerSource so this
// can never drift from what the dispatcher enforces), and therefore how many cycles
// the sweep needs.
//
// Reports 0/0 when no throughput accessor is attached (unit tests, and any caller
// that has not wired one): an unknown cost is stated as unknown rather than
// invented.
func (s *Service) redownloadCost(ctx context.Context, matched int) (perCycle, cycles int) {
	if s.throughput == nil {
		return 0, 0
	}
	perCycle = download.BatchPerSource(s.throughput.DownloadConcurrency(ctx))
	if perCycle < 1 {
		return 0, 0
	}
	// Ceiling division: a remainder still costs a whole cycle.
	return perCycle, (matched + perCycle - 1) / perCycle
}

// redownloadPredicates builds the Chapter predicate set behind BOTH the preview and
// the apply, so the two can never select different rows: a DOWNLOADED chapter whose
// CBZ was written at or after the cutoff by a provider of the filter's source.
//
// The date column is download_date and nothing else — see RedownloadFilter.Since
// for why first_downloaded_at is the wrong key.
func redownloadPredicates(filter RedownloadFilter) []predicate.Chapter {
	return []predicate.Chapter{
		entchapter.StateEQ(entchapter.StateDownloaded),
		entchapter.DownloadDateGTE(filter.Since),
		entchapter.HasSatisfiedByWith(redownloadSourcePredicates(filter)...),
	}
}

// redownloadSourcePredicates matches the SeriesProvider rows that count as "the
// filter's source", resolving the canonical source name across BOTH provider create
// paths: a live-ingested row carries the display name in provider_name (with the
// numeric source id in provider), while a disk-reconciled row leaves provider_name
// empty and stores the display name in provider. That split is the same one the
// library merge machinery exists to reconcile, so a re-download must honour both or
// it would silently skip every disk-origin source.
//
// KNOWN-MINOR: the comparison is exact, whereas breakerKey TRIMS the stored value —
// a provider name persisted with surrounding whitespace would be listed under its
// trimmed name yet not match here. The same divergence already breaks the breaker
// join, so it is a data defect rather than a filter one.
func redownloadSourcePredicates(filter RedownloadFilter) []predicate.SeriesProvider {
	name := strings.TrimSpace(filter.Source)
	preds := []predicate.SeriesProvider{
		entseriesprovider.Or(
			entseriesprovider.ProviderNameEQ(name),
			entseriesprovider.And(
				entseriesprovider.ProviderNameEQ(""),
				entseriesprovider.ProviderEQ(name),
			),
		),
	}
	if filter.Scanlator != nil {
		preds = append(preds, entseriesprovider.ScanlatorEQ(*filter.Scanlator))
	}
	return preds
}
