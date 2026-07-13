package metadata

import (
	"context"
	"log/slog"

	"golang.org/x/sync/errgroup"
)

// fanoutConcurrency bounds how many providers Search/Identify query in
// parallel. The registry holds at most a handful of providers (5 in the
// production set — see internal/metadata/providers), so this is generous
// headroom rather than a tight throttle; it mirrors internal/imports's
// searchConcurrency bound on the same shape of fan-out.
const fanoutConcurrency = 8

// Provider looks up a registered provider by its Key(). The second return
// value reports whether one was found.
func (r *Registry) Provider(key string) (Provider, bool) {
	for _, p := range r.providers {
		if p.Key() == key {
			return p, true
		}
	}
	return nil, false
}

// selectProviders returns the registry's providers in registration order
// (registration order IS priority order by convention — see
// internal/metadata/providers.NewRegistry), filtered to keys when keys is
// non-empty (empty ⇒ every provider). A key with no matching provider is
// silently ignored; Provider(key) is the place to detect an unknown key
// explicitly.
func (r *Registry) selectProviders(keys []string) []Provider {
	if len(keys) == 0 {
		return r.providers
	}
	want := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		want[k] = struct{}{}
	}
	out := make([]Provider, 0, len(r.providers))
	for _, p := range r.providers {
		if _, ok := want[p.Key()]; ok {
			out = append(out, p)
		}
	}
	return out
}

// Search fans a free-text query out to the selected providers (all
// registered providers when keys is empty) with bounded concurrency
// (fanoutConcurrency) and returns their combined SearchResult hits.
//
// Results are grouped by REGISTRY ORDER, never goroutine-completion order —
// each provider's hits are written to its own slot in an index-aligned
// slice, so the final concatenation is deterministic across runs. A
// provider's Search failure is logged at WARN and the provider is SKIPPED:
// Search never fails outright because one source misbehaves, mirroring
// internal/imports.Service.Search's partial-results contract. limit is
// passed as 0 to every provider's Search, so each falls back to its own
// documented default (see e.g. mangaupdates.Client.Search's doc comment).
func (r *Registry) Search(ctx context.Context, q string, keys []string) ([]SearchResult, error) {
	providers := r.selectProviders(keys)

	hits := make([][]SearchResult, len(providers))

	sem := make(chan struct{}, fanoutConcurrency)
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range providers {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return nil // deadline/cancel: drop this provider, never surface as an error
			}
			defer func() { <-sem }()

			results, err := p.Search(gctx, q, 0)
			if err != nil {
				slog.WarnContext(gctx, "metadata: provider search failed",
					"provider", p.Key(), "err", err)
				return nil
			}
			hits[i] = results
			return nil
		})
	}
	_ = g.Wait() // every goroutine returns nil; failures/deadlines are per-provider skips, not fan-out errors

	var out []SearchResult
	for _, h := range hits {
		out = append(out, h...)
	}
	return out, nil
}

// ProviderMatch is one provider's confident match for an Identify query —
// the per-provider evidence behind the merged result, kept so the owner
// can see (and later re-pick) which remote record each field came from.
type ProviderMatch struct {
	ProviderKey string
	RemoteID    string
	RemoteURL   string
	Title       string
	CoverURL    string
	Year        int
}

// IdentifyResult is Identify's return value: the merged SeriesMetadata,
// the per-provider matches that fed it, and the priority Order Merge
// walked to produce it (Order[0] is the primary — the caller uses it as
// the metadata_source). All three are zero-valued together when no
// provider matched.
type IdentifyResult struct {
	Merged  SeriesMetadata
	Matches []ProviderMatch
	Order   []string
}

// identifyOutcome is one provider's Identify result, written to a fixed
// slot of an index-aligned slice by the fan-out below so results can be
// walked back out in registry (priority) order without a mutex.
type identifyOutcome struct {
	match ProviderMatch
	meta  SeriesMetadata
	ok    bool
}

// Identify runs Match against every selected provider (all registered
// providers when keys is empty), fetches full metadata for every
// confident match, and merges them via Merge with Order set to the
// matched providers in REGISTRY PRIORITY order (index 0 = primary, per
// the Provider.Priority() convention documented on provider.go). The
// caller reads Order[0] as the resolved metadata_source.
//
// A provider is SKIPPED — logged at WARN, never surfaced as an error —
// when its Match call errors, it reports no confident match (nil, nil),
// or the follow-up GetSeriesMetadata call for its match errors: one
// misbehaving or uncertain provider must never sink the whole identify.
// When no provider matches, Identify returns a zero-value IdentifyResult
// and a nil error — "no match anywhere" is an expected outcome the caller
// renders as "not found", not a failure.
func (r *Registry) Identify(ctx context.Context, q MatchQuery, keys []string) (IdentifyResult, error) {
	providers := r.selectProviders(keys)

	outcomes := make([]identifyOutcome, len(providers))

	sem := make(chan struct{}, fanoutConcurrency)
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range providers {
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-gctx.Done():
				return nil // deadline/cancel: drop this provider, never surface as an error
			}
			defer func() { <-sem }()

			outcomes[i] = matchProvider(gctx, p, q)
			return nil
		})
	}
	_ = g.Wait() // every goroutine returns nil; failures/misses are per-provider skips, not fan-out errors

	metas := make(map[string]SeriesMetadata)
	var order []string
	var matches []ProviderMatch
	for _, o := range outcomes {
		if !o.ok {
			continue
		}
		metas[o.match.ProviderKey] = o.meta
		order = append(order, o.match.ProviderKey)
		matches = append(matches, o.match)
	}

	if len(order) == 0 {
		return IdentifyResult{}, nil
	}

	merged := Merge(MergeInput{Metas: metas, Order: order})
	return IdentifyResult{Merged: merged, Matches: matches, Order: order}, nil
}

// matchProvider runs one provider's Match + GetSeriesMetadata pair for
// Identify's fan-out, returning a zero-value (ok=false) identifyOutcome on
// any failure or "no confident match" — the caller logs nothing itself,
// this is where the per-provider skip is logged, keeping Identify's own
// body free of the log-and-continue boilerplate for both failure sites.
func matchProvider(ctx context.Context, p Provider, q MatchQuery) identifyOutcome {
	hit, err := p.Match(ctx, q)
	if err != nil {
		slog.WarnContext(ctx, "metadata: provider match failed", "provider", p.Key(), "err", err)
		return identifyOutcome{}
	}
	if hit == nil {
		return identifyOutcome{}
	}

	meta, err := p.GetSeriesMetadata(ctx, hit.RemoteID)
	if err != nil {
		slog.WarnContext(ctx, "metadata: provider metadata fetch failed",
			"provider", p.Key(), "remote_id", hit.RemoteID, "err", err)
		return identifyOutcome{}
	}

	return identifyOutcome{
		match: ProviderMatch{
			ProviderKey: p.Key(),
			RemoteID:    hit.RemoteID,
			RemoteURL:   hit.URL,
			Title:       hit.Title,
			CoverURL:    hit.CoverURL,
			Year:        hit.Year,
		},
		meta: meta,
		ok:   true,
	}
}
