// Package ingest — test-only exports.
//
// This file is compiled only during `go test`; nothing in it is visible in the
// production binary. It exposes a clock-injectable ChapterCache constructor so
// black-box tests can drive TTL expiry and TTL hot reload deterministically
// (advance a fake clock / mutate the TTL provider) instead of sleeping.
package ingest

import (
	"context"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

// NewChapterCacheClock builds a ChapterCache with an injectable TTL provider AND
// an injectable clock, so black-box tests can drive both TTL expiry and TTL hot
// reload deterministically. Production uses NewChapterCache (clock = time.Now).
func NewChapterCacheClock(ttl func(context.Context) time.Duration, now func() time.Time) *ChapterCache {
	c := NewChapterCache(ttl)
	c.now = now
	return c
}

// MapToFetchedChapters exposes the unexported mapToFetchedChapters mapper so
// black-box tests can pin its field-mapping deltas (notably the reversed
// ProviderIndex direction — see its doc comment) without going through the
// full AddSeries/testdb round-trip.
func MapToFetchedChapters(chs []sourceengine.Chapter, scanlator string) []chapter.FetchedChapter {
	return mapToFetchedChapters(chs, scanlator)
}
