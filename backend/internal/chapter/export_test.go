// Package chapter — test-only exports. This file is compiled only during
// `go test`; nothing in it is visible in the production binary.
package chapter

import (
	"context"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
)

// AbsorbProviderChapterRace exposes the unexported absorbProviderChapterRace
// function so that integration tests in package chapter_test can exercise the
// concurrent-INSERT race path deterministically without relying on real goroutine
// races.
func AbsorbProviderChapterRace(
	ctx context.Context,
	client *ent.Client,
	seriesProviderID uuid.UUID,
	key string,
	fc FetchedChapter,
) error {
	return absorbProviderChapterRace(ctx, client, seriesProviderID, key, fc)
}
