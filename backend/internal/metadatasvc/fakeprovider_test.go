package metadatasvc_test

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/technobecet/tsundoku/internal/metadata"
)

// fakeProvider is a minimal, fully-configurable metadata.Provider double for
// exercising metadatasvc.Service without any network access — mirrors
// internal/metadata's own registry_test.go fakeProvider (same shape, kept as
// a package-local copy since metadatasvc_test is a separate black-box
// package and test doubles are not exported production code).
type fakeProvider struct {
	key      string
	id       int
	priority int

	searchResults []metadata.SearchResult
	searchErr     error
	searchCalls   int32 // atomic

	matchResult *metadata.SearchResult
	matchErr    error

	// metas is keyed by remoteID so GetSeriesMetadata returns the right
	// record for whatever RemoteID a test drives it with.
	metas   map[string]metadata.SeriesMetadata
	metaErr error
}

var _ metadata.Provider = (*fakeProvider)(nil)

func (f *fakeProvider) Key() string   { return f.key }
func (f *fakeProvider) ID() int       { return f.id }
func (f *fakeProvider) Priority() int { return f.priority }

func (f *fakeProvider) Search(_ context.Context, _ string, _ int) ([]metadata.SearchResult, error) {
	atomic.AddInt32(&f.searchCalls, 1)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchResults, nil
}

func (f *fakeProvider) GetSeriesMetadata(_ context.Context, remoteID string) (metadata.SeriesMetadata, error) {
	if f.metaErr != nil {
		return metadata.SeriesMetadata{}, f.metaErr
	}
	m, ok := f.metas[remoteID]
	if !ok {
		return metadata.SeriesMetadata{}, errors.New("fakeProvider: no metadata for remote id " + remoteID)
	}
	return m, nil
}

func (f *fakeProvider) GetSeriesCover(_ context.Context, _ string) ([]byte, string, error) {
	return nil, "", errors.New("fakeProvider: GetSeriesCover not implemented")
}

func (f *fakeProvider) Match(_ context.Context, _ metadata.MatchQuery) (*metadata.SearchResult, error) {
	if f.matchErr != nil {
		return nil, f.matchErr
	}
	return f.matchResult, nil
}
