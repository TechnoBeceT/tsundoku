package metadatasvc_test

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// fakeSourceCoverFetcher is a minimal metadatasvc.SourceCoverFetcher double:
// it returns canned bytes/ext for exactly the (seriesID, providerID) pair it
// was seeded with, and a distinguishable error for anything else — so a test
// asserting SetCover's "source" branch resolves the RIGHT provider (not just
// any provider) has something to fail on.
type fakeSourceCoverFetcher struct {
	seriesID   uuid.UUID
	providerID uuid.UUID
	data       []byte
	ext        string

	// err, when non-nil, is returned unconditionally instead of data/ext —
	// the seam TestSetCover_SourcePortFailurePropagates needs.
	err error

	calls int
}

func (f *fakeSourceCoverFetcher) SourceCoverBytes(_ context.Context, seriesID, providerID uuid.UUID) ([]byte, string, error) {
	f.calls++
	if f.err != nil {
		return nil, "", f.err
	}
	if seriesID != f.seriesID || providerID != f.providerID {
		return nil, "", errors.New("fakeSourceCoverFetcher: unexpected series/provider id")
	}
	return f.data, f.ext, nil
}
