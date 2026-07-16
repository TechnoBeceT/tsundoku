package sourceengine

import "context"

// SourceLister adapts a Client into the series-health "loaded source set" port
// (series.SourceLister): it lists the engine's currently-loaded source ids as
// an int64 set so the health scan can flag a provider whose extension was
// uninstalled as unavailable. It lives here (not in the series domain) so the
// series package stays free of engine types while both wiring sites — the HTTP
// routes and the refresh-driven UnhealthyCount — share ONE adapter (§2 DRY)
// rather than re-implementing the same list loop.
type SourceLister struct {
	client Client
}

// NewSourceLister wraps a Client so it satisfies series.SourceLister.
func NewSourceLister(client Client) *SourceLister {
	return &SourceLister{client: client}
}

// LoadedSourceIDs returns the set of the engine's currently-loaded source ids,
// keyed by their numeric source id, with ok=true on a successful Sources call.
// A Sources failure returns ok=false and the error so the caller fails safe
// (flags nothing) rather than treating every source as missing. Source.ID is
// already an int64, so the set is built directly (no string parse).
func (l *SourceLister) LoadedSourceIDs(ctx context.Context) (map[int64]struct{}, bool, error) {
	sources, err := l.client.Sources(ctx)
	if err != nil {
		return nil, false, err
	}
	set := make(map[int64]struct{}, len(sources))
	for _, src := range sources {
		set[src.ID] = struct{}{}
	}
	return set, true, nil
}
