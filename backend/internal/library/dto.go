package library

import "github.com/technobecet/tsundoku/internal/disk"

// FoundSeriesDTO is one row of a Scan's staging result — a series discovered
// on disk (whether or not it is already imported into the DB).
type FoundSeriesDTO struct {
	Path         string   `json:"path"`
	Title        string   `json:"title"`
	Category     string   `json:"category"`
	ChapterCount int      `json:"chapterCount"`
	Providers    []string `json:"providers"`
	Status       string   `json:"status"`
	AlreadyInDB  bool     `json:"alreadyInDb"`
}

// storedFacts is the concrete JSON shape saved in ImportEntry.found.
type storedFacts struct {
	Facts disk.SeriesFacts `json:"facts"`
}

// ProviderRef identifies one Suwayomi source+manga+scanlator to attach to an
// existing series via AddProviders (Slice P batch attach), including at
// Import time via a matches list. It carries no importance — AddProviders
// assigns importances itself, below the series' existing providers
// (decision E, belowExistingImportances).
type ProviderRef struct {
	Source    string `json:"source"`
	MangaID   int    `json:"mangaId"`
	Scanlator string `json:"scanlator"`
}
