package imports

import "context"

// SearchStream emits accumulated results without waiting for slower sources. Emissions are
// serialized per consumer. A consumer failure releases only that caller's upstream demand.
func (s *Service) SearchStream(ctx context.Context, query string, sourceIDs []string, emit func(SearchSnapshotDTO) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.searchCache == nil {
		_, err := s.searchUncached(ctx, query, sourceIDs, emit)
		return err
	}
	_, err := s.searchCache.Get(ctx, query, sourceIDs, func(workCtx context.Context, publish func(SearchSnapshotDTO) error) ([]SearchGroupDTO, error) {
		return s.searchUncached(workCtx, query, sourceIDs, publish)
	}, emit)
	return err
}

func emitSearchSnapshot(emit func(SearchSnapshotDTO) error, snapshot SearchSnapshotDTO) error {
	if emit == nil {
		return nil
	}
	return emit(snapshot)
}
