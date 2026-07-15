package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/series"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// sourceCoverAdapter implements metadatasvc.SourceCoverFetcher over the real
// engine-host client plus the series domain's own per-provider cover
// resolution (series.Service.ProviderCoverURL — the SAME series↔provider
// ownership check the per-provider cover proxy route performs, see
// handler/series.ProviderCover). It lives at the composition root, not
// inside internal/metadatasvc, so that package's own declared import
// surface (metadata + ent + disk + category) stays accurate — see
// metadatasvc.SourceCoverFetcher's doc comment for the full rationale.
type sourceCoverAdapter struct {
	series *series.Service
	sw     sourceengine.Client
}

// SourceCoverBytes resolves providerID's stored cover_url + numeric engine
// source id (failing with series.ErrSeriesNotFound / series.ErrProviderNotInSeries
// / series.ErrNoCover / series.ErrCoverFetchFailed when it does not resolve —
// e.g. a stale providerId the owner's picker held after a source was removed,
// or a disk-origin provider with no engine source at all) and fetches those
// bytes from the engine host: the identical two steps handler/series.
// ProviderCover performs for the per-provider cover proxy, so a
// metadata-engine "source" cover pick fetches the exact same bytes the owner
// already sees in the metadata-source picker's thumbnail.
func (a sourceCoverAdapter) SourceCoverBytes(ctx context.Context, seriesID, providerID uuid.UUID) ([]byte, string, error) {
	coverURL, sourceID, err := a.series.ProviderCoverURL(ctx, seriesID, providerID)
	if err != nil {
		return nil, "", err
	}
	return a.sw.Image(ctx, sourceID, "", coverURL)
}
