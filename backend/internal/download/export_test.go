package download

import (
	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/fetcher"
)

// BuildRenderMeta exposes the unexported buildRenderMeta for black-box tests
// that verify the Chapter/ProviderChapter/SeriesProvider → RenderMeta mapping
// (notably the ProviderLabel resolution) without a running database.
func BuildRenderMeta(ch *ent.Chapter, pc *ent.ProviderChapter, sp *ent.SeriesProvider, maxChapter *float64) disk.RenderMeta {
	return buildRenderMeta(ch, pc, sp, maxChapter)
}

// ProgressSink exposes the unexported progressSink for black-box tests that
// exercise the throttle rule directly (no DB required — the sink only needs the
// Dispatcher's hub to broadcast).
func (d *Dispatcher) ProgressSink(chapterID uuid.UUID, state string) fetcher.ProgressFunc {
	return d.progressSink(chapterID, state)
}
