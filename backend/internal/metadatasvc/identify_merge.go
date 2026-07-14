package metadatasvc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	entseries "github.com/technobecet/tsundoku/internal/ent/series"
	"github.com/technobecet/tsundoku/internal/metadata"
)

// Selection is one owner-picked (provider, remoteID) candidate in a
// multi-select IdentifyMerge call. Order matters: the FIRST successfully
// fetched selection becomes the primary — the scalar (title/description/
// status/year/publisher) gap-fill anchor and the record written to
// Series.metadata_source (QCAT-228).
type Selection struct {
	// Provider is the metadata Provider's Key() (e.g. "anilist").
	Provider string
	// RemoteID is the provider's own identifier for the picked series.
	RemoteID string
}

// IdentifyMerge performs the owner's MULTI-SELECT identify: the owner picks
// SEVERAL correct matches (unlike single-arg Identify, which auto-matches
// every OTHER provider by the primary's title) and this call fetches and
// merges EXACTLY the selections given — no auto-matching beyond the owner's
// own picks. It reuses the same metadata.Merge union-then-gap-fill kernel
// (QCAT-228) and the same persist tail as Identify, so the wire format lands
// identically regardless of how the merge inputs were gathered.
//
// selections[0] is always the intended primary. If its own fetch fails, the
// FIRST selection that DOES succeed becomes the effective primary (order is
// preserved through the whole selections slice, filtered to successes) — see
// the per-provider-failure rule below.
//
// Per-provider fetch failure is NON-FATAL: an unknown provider key or a
// failed GetSeriesMetadata call is logged and that selection is skipped, and
// the merge proceeds with whatever succeeded (partial merge) — mirrors
// Identify's own aggregate-provider skip behavior. Only when EVERY selection
// fails does IdentifyMerge return ErrAllSelectionsFailed (naming which
// providers failed, §16).
//
// On success the series' Series.metadata_locked is set true — this is
// hand-curation: an explicit owner multi-pick must never be silently
// overwritten by a later AutoIdentify background pass (see AutoIdentify's own
// doc comment for the corresponding guard).
func (s *Service) IdentifyMerge(ctx context.Context, seriesID uuid.UUID, selections []Selection) error {
	if len(selections) == 0 {
		return fmt.Errorf("metadatasvc: identify merge for series %s: %w", seriesID, ErrNoSelections)
	}

	exists, err := s.client.Series.Query().Where(entseries.IDEQ(seriesID)).Exist(ctx)
	if err != nil {
		return fmt.Errorf("metadatasvc: check series %s: %w", seriesID, err)
	}
	if !exists {
		return ErrSeriesNotFound
	}

	metas := make(map[string]metadata.SeriesMetadata, len(selections))
	order := make([]string, 0, len(selections))
	var failed []string
	for _, sel := range selections {
		provider, ok := s.registry.Provider(sel.Provider)
		if !ok {
			failed = append(failed, sel.Provider+" (unknown provider)")
			slog.WarnContext(ctx, "metadatasvc: identify merge: unknown provider, skipping",
				"series_id", seriesID, "provider", sel.Provider)
			continue
		}
		meta, ferr := provider.GetSeriesMetadata(ctx, sel.RemoteID)
		if ferr != nil {
			failed = append(failed, sel.Provider)
			slog.WarnContext(ctx, "metadatasvc: identify merge: fetch failed, skipping",
				"series_id", seriesID, "provider", sel.Provider, "remote_id", sel.RemoteID, "err", ferr)
			continue
		}
		metas[sel.Provider] = meta
		order = append(order, sel.Provider)
	}
	if len(order) == 0 {
		return fmt.Errorf("metadatasvc: identify merge for series %s: every selected provider failed (%s): %w",
			seriesID, strings.Join(failed, ", "), ErrAllSelectionsFailed)
	}

	merged := metadata.Merge(metadata.MergeInput{Metas: metas, Order: order})

	primaryKey := order[0]
	primaryProvider, _ := s.registry.Provider(primaryKey) // guaranteed present: it just succeeded above.
	primaryMeta := metas[primaryKey]
	primaryRemoteID := remoteIDFor(selections, primaryKey)

	src := metadata.SourceRef{
		Kind:      "metadata",
		Ref:       primaryKey,
		RemoteID:  primaryRemoteID,
		RemoteURL: resolvePrimaryURL(ctx, primaryProvider, primaryMeta.Title, primaryRemoteID),
	}

	return s.persist(ctx, seriesID, merged, src, primaryMeta.CoverURL, true)
}

// remoteIDFor returns the RemoteID the owner picked for providerKey — the
// FIRST matching selection in original order (a duplicate-provider pick is an
// edge case the UI never produces, but "first wins" keeps this deterministic
// rather than panicking). Returns "" if providerKey names no selection (never
// happens for a key IdentifyMerge derived from selections itself).
func remoteIDFor(selections []Selection, providerKey string) string {
	for _, sel := range selections {
		if sel.Provider == providerKey {
			return sel.RemoteID
		}
	}
	return ""
}
