package library

import "context"

// BatchFailure records one path's failure within a batch import — the path
// that failed and the underlying error's message (never the raw error, so
// the DTO stays a plain JSON-safe shape at the handler boundary).
type BatchFailure struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// BatchResult is the outcome of ImportBatch: how many paths imported
// cleanly, and the per-path failures (if any) for the rest.
type BatchResult struct {
	Imported int            `json:"imported"`
	Failed   []BatchFailure `json:"failed"`
}

// ImportBatch disk-only imports every path in paths, one at a time via the
// existing single-path Import (see import.go) with no match — this is the
// "import all remaining as disk-only" bulk action for a 1000+ series
// migration, so an owner doesn't have to fire N sequential requests.
//
// Partial success: a bad path (unstaged, or any other Import failure) is
// recorded in Failed and does NOT abort the rest of the batch — the whole
// point of a bulk action is that one bad row can't block the other 999.
// ImportBatch itself only returns a non-nil error if it cannot proceed at
// all (there is no such case today; every per-path failure is captured in
// the returned BatchResult instead).
func (s *Service) ImportBatch(ctx context.Context, paths []string) (BatchResult, error) {
	result := BatchResult{Failed: []BatchFailure{}}
	for _, path := range paths {
		if _, err := s.Import(ctx, path, nil); err != nil {
			result.Failed = append(result.Failed, BatchFailure{Path: path, Message: err.Error()})
			continue
		}
		result.Imported++
	}
	return result, nil
}
