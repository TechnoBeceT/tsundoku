package library

import (
	"context"

	"github.com/technobecet/tsundoku/internal/imports"
)

// MatchCandidates searches Suwayomi sources for a staged ImportEntry's title
// so the owner can pick a source to attach when calling Import. It reuses
// loadEntryByPath (import.go) for the same not-found translation Import
// uses, then fans the entry's title out across the sources via
// imports.Service.Search.
//
// sourceIDs optionally restricts the fan-out to a named subset of sources
// (from the caller's ?sources filter); nil/empty searches every source.
// Unknown IDs are silently dropped by the imports service (same contract as
// GET /api/search).
//
// ErrEntryNotFound is returned when path does not match any ImportEntry
// staged by a prior Scan.
func (s *Service) MatchCandidates(ctx context.Context, path string, sourceIDs []string) ([]imports.SearchGroupDTO, error) {
	entry, err := s.loadEntryByPath(ctx, path)
	if err != nil {
		return nil, err
	}
	return s.imports.Search(ctx, entry.Title, sourceIDs)
}

// MatchCandidatesProgress searches using the staged entry's server-owned title and emits
// accumulated candidates as sources finish. An unknown path is rejected before any snapshot.
func (s *Service) MatchCandidatesProgress(ctx context.Context, path string, sourceIDs []string, emit func(imports.SearchSnapshotDTO) error) error {
	entry, err := s.loadEntryByPath(ctx, path)
	if err != nil {
		return err
	}
	return s.imports.SearchStream(ctx, entry.Title, sourceIDs, emit)
}
