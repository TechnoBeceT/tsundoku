// Package chapter contains the chapter domain: state machine, ingest, and
// service helpers used by the M1 download dispatcher and upgrade engine.
package chapter

import (
	"context"
	"fmt"
	"sort"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
	entchapter "github.com/technobecet/tsundoku/internal/ent/chapter"
	entproviderchapter "github.com/technobecet/tsundoku/internal/ent/providerchapter"
	entseriesprovider "github.com/technobecet/tsundoku/internal/ent/seriesprovider"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
)

// WantedChapters returns up to limit Chapter rows the download dispatcher should
// consider this cycle: every chapter in state wanted OR failed.
//
//   - wanted    — freshly discovered, or reset by an owner retry.
//   - failed    — at least one source has failed on it, but not every source is
//     exhausted yet, so it is still retryable in the background.
//
// The per-source retry gating (which SOURCE is a live candidate right now) is NOT
// done here — it lives in RankedLiveCandidates and is applied per chapter by the
// dispatcher's Process. Returning a failed chapter whose sources are all on
// cooldown is harmless: Process finds no live candidate and leaves it untouched.
// This keeps the "live candidate" rule defined in exactly one place (§2 DRY)
// instead of being half-encoded in a SQL predicate here.
//
// downloaded / upgrade_available / downloading / permanently_failed chapters are
// deliberately excluded — the upgrade engine owns the first two, the third is
// in-flight, and the last is terminal until an owner retry moves it back to wanted.
//
// Results are ordered by chapter number ascending (nulls last), with the random
// UUID id as a stable tiebreaker, so chapters download 1, 2, 3, … rather than in
// the effectively-random id order (id is a UUIDv4). A chapter with no parsed
// number sorts last but stays reachable.
func WantedChapters(ctx context.Context, client *ent.Client, limit int) ([]*ent.Chapter, error) {
	chapters, err := client.Chapter.Query().
		Where(entchapter.StateIn(
			entchapter.StateWanted,
			entchapter.StateFailed,
		)).
		Order(
			entchapter.ByNumber(sql.OrderAsc(), sql.OrderNullsLast()),
			entchapter.ByID(),
		).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("chapter.WantedChapters: query: %w", err)
	}
	return chapters, nil
}

// Candidate pairs a ProviderChapter (the per-source availability + retry-state
// row) with its owning SeriesProvider. It is one source the dispatcher may try
// to download a chapter from. SeriesProvider.Importance is the ranking key
// (higher = better).
type Candidate struct {
	// ProviderChapter is the per-(source, chapter) row carrying the source's URL,
	// suwayomi id, and per-source retry state (attempts / last_error / next_attempt_at).
	ProviderChapter *ent.ProviderChapter
	// SeriesProvider is the owning source (provider + scanlator + importance).
	SeriesProvider *ent.SeriesProvider
}

// providerChaptersForKey loads every ProviderChapter (with its SeriesProvider
// edge) that offers ch's chapter_key within ch's series, MINUS the sources the
// owner has flagged as fractional re-uploaders for a fractional chapter (see
// dropIgnoredFractionalSources). It is the single join shared by
// BestProviderChapter, RankedLiveCandidates, HasAnyProviderChapter, and
// AllProvidersExhausted so the "which sources offer this chapter" question is
// answered in exactly one place (§2 DRY) — and so every one of them agrees about
// a suppressed source rather than half of them seeing it.
func providerChaptersForKey(ctx context.Context, client *ent.Client, ch *ent.Chapter) ([]*ent.ProviderChapter, error) {
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
		return nil, fmt.Errorf("chapter: query provider chapters for chapter %s (key=%q): %w", ch.ID, ch.ChapterKey, err)
	}
	return dropIgnoredFractionalSources(pcs, ch), nil
}

// dropIgnoredFractionalSources removes the sources the owner has flagged as
// fractional re-uploaders (SeriesProvider.ignore_fractional) — but ONLY for a
// FRACTIONAL chapter. A whole chapter is unaffected: the toggle suppresses a
// source's fractional re-uploads (a mirror republishing whole chapter N as a lone
// "N.1" under its own URL), it does not disable the source.
//
// There is deliberately NO HEURISTIC here. The engine cannot tell a re-upload from
// a genuine side-chapter: a source hosting a `5.5` omake obviously also hosts
// chapter 5, and `.5` is the MOST COMMON fractional in a real library (825 chapters
// across 44 series in the owner's). Any automatic rule would have suppressed — and
// then deleted — all of them. Suppression is reachable ONLY through the explicitly
// ticked per-(series, provider) flag.
//
// Applied inside providerChaptersForKey (the ONE shared join) so candidacy,
// exhaustion, and best-source all see the same set. Purely in-memory over the
// already-loaded SeriesProvider edge — no extra query. It EXCLUDES rows; it NEVER
// deletes them (never-auto-delete: un-ticking the flag restores the source
// immediately, from the very same rows).
func dropIgnoredFractionalSources(pcs []*ent.ProviderChapter, ch *ent.Chapter) []*ent.ProviderChapter {
	// A chapter with no parsed number cannot be judged fractional — leave it alone
	// rather than silently orphan it.
	if ch.Number == nil || !chapterrange.IsFractional(*ch.Number) {
		return pcs
	}
	out := make([]*ent.ProviderChapter, 0, len(pcs))
	for _, pc := range pcs {
		if sp := pc.Edges.SeriesProvider; sp != nil && sp.IgnoreFractional {
			continue
		}
		out = append(out, pc)
	}
	return out
}

// isExhausted reports whether a source has spent its whole per-source retry
// budget on this chapter (attempts >= maxRetries) and must not be tried again
// until an owner retry resets it.
func isExhausted(pc *ent.ProviderChapter, maxRetries int) bool {
	return pc.Attempts >= maxRetries
}

// isLiveCandidate reports whether a source is a download candidate for this
// chapter RIGHT NOW: it still has retry budget (not exhausted) AND it is past its
// per-source backoff cooldown (next_attempt_at is nil or already elapsed).
func isLiveCandidate(pc *ent.ProviderChapter, maxRetries int, now time.Time) bool {
	if isExhausted(pc, maxRetries) {
		return false
	}
	return pc.NextAttemptAt == nil || !pc.NextAttemptAt.After(now)
}

// RankedLiveCandidates returns the sources that may be tried for chapterID right
// now — those that have the chapter, still have retry budget (attempts <
// maxRetries), AND are past their per-source backoff cooldown (next_attempt_at nil
// or <= now) — sorted by importance DESC (best first), with the ProviderChapter id
// as a stable tiebreaker.
//
// BOTH the download dispatcher AND the upgrade engine use this single predicate:
//   - Download uses it to pick which source to fetch a wanted/failed chapter from.
//   - Upgrade uses it to pick a better source to swap a downloaded chapter to.
//
// The two paths differ only in what they WRITE on failure, never in candidacy: a
// download failure sticks (bumpSourceFailure increments attempts, so a source that
// truly can't deliver a chapter eventually exhausts and is dropped), whereas an
// upgrade failure only cools the source down (cooldownSource leaves attempts
// untouched), so a preferred source temporarily down during upgrade attempts never
// exhausts and always recovers as an upgrade target once it is back and past its
// cooldown.
//
// An empty slice means "nothing to act on this instant": a caller must then
// distinguish (via HasAnyProviderChapter / AllProvidersExhausted) between "no
// source offers this chapter yet", "every source is exhausted", and "sources exist
// but are all on cooldown".
func RankedLiveCandidates(ctx context.Context, client *ent.Client, chapterID uuid.UUID, maxRetries int, now time.Time) ([]Candidate, error) {
	ch, err := client.Chapter.Get(ctx, chapterID)
	if err != nil {
		return nil, fmt.Errorf("chapter.RankedLiveCandidates: load chapter %s: %w", chapterID, err)
	}

	pcs, err := providerChaptersForKey(ctx, client, ch)
	if err != nil {
		return nil, err
	}

	var out []Candidate
	for _, pc := range pcs {
		sp := pc.Edges.SeriesProvider
		if sp == nil {
			// Defensive path: WithSeriesProvider always loads the edge for a valid
			// FK, so a nil here means a broken FK — not reachable under normal operation.
			continue
		}
		if isLiveCandidate(pc, maxRetries, now) {
			out = append(out, Candidate{ProviderChapter: pc, SeriesProvider: sp})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SeriesProvider.Importance != out[j].SeriesProvider.Importance {
			return out[i].SeriesProvider.Importance > out[j].SeriesProvider.Importance
		}
		return out[i].ProviderChapter.ID.String() < out[j].ProviderChapter.ID.String()
	})
	return out, nil
}

// HasAnyProviderChapter reports whether at least one source offers chapterID's
// key within its series — i.e. whether any source has ever listed this chapter.
// The dispatcher uses it to tell "no source has this chapter yet" (leave the
// chapter wanted) apart from "sources exist but none can be tried right now".
//
// It goes through the shared providerChaptersForKey loader rather than a bespoke
// Count() so the ignore-fractional exclusion applies here TOO. It used to re-write
// the join's predicate inline, which would have made it the one reader that still
// saw a suppressed source: a fractional chapter whose only sources are all flagged
// re-uploaders is genuinely SOURCELESS, and the dispatcher must see it that way
// (handleNoCandidates → download.skip, stays wanted) rather than mistake it for a
// source merely on cooldown.
func HasAnyProviderChapter(ctx context.Context, client *ent.Client, chapterID uuid.UUID) (bool, error) {
	ch, err := client.Chapter.Get(ctx, chapterID)
	if err != nil {
		return false, fmt.Errorf("chapter.HasAnyProviderChapter: load chapter %s: %w", chapterID, err)
	}
	pcs, err := providerChaptersForKey(ctx, client, ch)
	if err != nil {
		return false, err
	}
	return len(pcs) > 0, nil
}

// AllProvidersExhausted reports whether chapterID has at least one source AND
// every one of those sources has spent its whole per-source retry budget
// (attempts >= maxRetries). This is the sole condition for permanent failure:
// the chapter is offered by one or more sources and none of them can deliver it
// anymore. A chapter with no sources returns false (it is not exhausted, it is
// simply awaiting a source via ingest).
func AllProvidersExhausted(ctx context.Context, client *ent.Client, chapterID uuid.UUID, maxRetries int) (bool, error) {
	ch, err := client.Chapter.Get(ctx, chapterID)
	if err != nil {
		return false, fmt.Errorf("chapter.AllProvidersExhausted: load chapter %s: %w", chapterID, err)
	}
	pcs, err := providerChaptersForKey(ctx, client, ch)
	if err != nil {
		return false, err
	}
	if len(pcs) == 0 {
		return false, nil
	}
	for _, pc := range pcs {
		if !isExhausted(pc, maxRetries) {
			return false, nil
		}
	}
	return true, nil
}

// BestProviderChapter returns the ProviderChapter for chapterID that is on the
// highest-importance SeriesProvider offering this chapter's key, IGNORING
// per-source retry state. A higher importance number means higher priority.
//
// It is the retry-agnostic pick (the plain "which source is best"), kept as a
// small public helper reimplemented over the shared providerChaptersForKey loader
// (§2 DRY). Callers that must respect per-source retry/cooldown state use
// RankedLiveCandidates instead. Returns an error if
// no source offers this chapter's key, or if the chapter cannot be loaded. The
// second return value is the importance of the selected SeriesProvider.
func BestProviderChapter(ctx context.Context, client *ent.Client, chapterID uuid.UUID) (*ent.ProviderChapter, int, error) {
	ch, err := client.Chapter.Get(ctx, chapterID)
	if err != nil {
		return nil, 0, fmt.Errorf("chapter.BestProviderChapter: load chapter %s: %w", chapterID, err)
	}

	pcs, err := providerChaptersForKey(ctx, client, ch)
	if err != nil {
		return nil, 0, err
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
