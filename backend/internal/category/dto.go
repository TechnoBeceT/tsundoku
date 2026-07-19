package category

import (
	"log/slog"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/ent"
)

// CategoryDTO is the wire shape for one library category: its identity, name,
// owner-chosen sort order, the isDefault flag (true for the single category new /
// uncategorized series land in — it can never be deleted, but ANY category can be
// renamed), and the number of series currently filed under it.
type CategoryDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
	IsDefault bool   `json:"isDefault"`
	Count     int    `json:"count"`
}

// newCategoryDTO maps an ent.Category plus its (separately computed) series
// count into a CategoryDTO.
func newCategoryDTO(c *ent.Category, count int) CategoryDTO {
	return CategoryDTO{
		ID:        c.ID.String(),
		Name:      c.Name,
		SortOrder: c.SortOrder,
		IsDefault: c.IsDefault,
		Count:     count,
	}
}

// NameOf returns a series' category folder name from its eagerly-loaded category
// edge, or "" when the series genuinely has no category (an unlinked legacy row
// before backfill). Exported so the download and downloads domains resolve the
// on-disk category through one definition (§2 DRY) instead of reaching into
// row.Edges.Category themselves.
//
// FOOTGUN GUARD: if the scalar category_id IS set but the edge was not loaded,
// the series DOES have a category and the caller simply forgot WithCategory().
// Returning "" silently in that case would mislocate the series on disk (it would
// fall back to "Other"), so NameOf emits a LOUD slog.Warn before returning "" —
// surfacing the missing eager-load instead of swallowing it. A truly category-less
// row (category_id == uuid.Nil) returns "" silently, which is the legitimate
// pre-backfill case and not a bug.
func NameOf(s *ent.Series) string {
	if s == nil {
		return ""
	}
	if s.Edges.Category != nil {
		return s.Edges.Category.Name
	}
	if s.CategoryID != uuid.Nil {
		slog.Warn("category.NameOf: category_id is set but the Category edge was not eager-loaded — caller must use WithCategory(); returning empty name",
			"series_id", s.ID,
			"category_id", s.CategoryID,
		)
	}
	return ""
}
