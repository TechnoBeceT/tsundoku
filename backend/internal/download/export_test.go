package download

import (
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/ent"
)

// BuildRenderMeta exposes the unexported buildRenderMeta for black-box tests
// that verify the Chapter/ProviderChapter/SeriesProvider → RenderMeta mapping
// (notably the ProviderLabel resolution) without a running database.
func BuildRenderMeta(ch *ent.Chapter, pc *ent.ProviderChapter, sp *ent.SeriesProvider, maxChapter *float64) disk.RenderMeta {
	return buildRenderMeta(ch, pc, sp, maxChapter)
}
