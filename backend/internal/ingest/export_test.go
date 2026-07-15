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
)

// NewChapterCacheClock builds a ChapterCache with an injectable TTL provider AND
// an injectable clock, so black-box tests can drive both TTL expiry and TTL hot
// reload deterministically. Production uses NewChapterCache (clock = time.Now).
func NewChapterCacheClock(ttl func(context.Context) time.Duration, now func() time.Time) *ChapterCache {
	c := NewChapterCache(ttl)
	c.now = now
	return c
}
