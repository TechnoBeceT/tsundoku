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

// MatchInput describes an owner-chosen Suwayomi source to attach to a staged
// ImportEntry at import time.
type MatchInput struct {
	Source     string `json:"source"`
	MangaID    int    `json:"mangaId"`
	Importance int    `json:"importance"`
}
