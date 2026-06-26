package category

import "github.com/technobecet/tsundoku/internal/ent"

// CategoryDTO is the wire shape for one library category: its identity, name,
// owner-chosen sort order, the protected flag (true only for the default
// "Other"), and the number of series currently filed under it.
type CategoryDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sortOrder"`
	Protected bool   `json:"protected"`
	Count     int    `json:"count"`
}

// newCategoryDTO maps an ent.Category plus its (separately computed) series
// count into a CategoryDTO.
func newCategoryDTO(c *ent.Category, count int) CategoryDTO {
	return CategoryDTO{
		ID:        c.ID.String(),
		Name:      c.Name,
		SortOrder: c.SortOrder,
		Protected: c.Protected,
		Count:     count,
	}
}

// NameOf returns a series' category folder name from its eagerly-loaded
// category edge, or "" when the edge is absent (an unlinked legacy row before
// backfill, or a query that did not load the edge). Exported so the download
// and downloads domains resolve the on-disk category through one definition
// (§2 DRY) instead of reaching into row.Edges.Category themselves.
func NameOf(s *ent.Series) string {
	if s != nil && s.Edges.Category != nil {
		return s.Edges.Category.Name
	}
	return ""
}
