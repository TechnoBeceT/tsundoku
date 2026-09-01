package library

import (
	"github.com/technobecet/tsundoku/internal/disk"
	"github.com/technobecet/tsundoku/internal/sourceengine"
)

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

// ProviderRef identifies one engine-host source+manga+scanlator to attach to
// an existing series via AddProviders (Slice P batch attach), including at
// Import time via a matches list. It carries no importance — AddProviders
// assigns importances itself, below the series' existing providers
// (decision E, belowExistingImportances).
//
// MangaID + URL (P2 Suwayomi-removal, slice 3b): the backend is now
// URL-addressed — URL is what AddProvider actually uses to fetch the manga.
// MangaID is KEPT, additive-only, so the not-yet-updated frontend still
// typechecks against the OpenAPI-generated client; the backend no longer
// reads it.
type ProviderRef struct {
	Source string `json:"source"`
	// MangaID is UNUSED by the backend (prefer URL) — retained only for FE
	// wire compatibility until slice 3b-FE switches to URL.
	MangaID int `json:"mangaId"`
	// URL is the exact source-owned serialized manga address. Its shape may be
	// relative, opaque, or absolute cross-origin.
	URL         string                   `json:"url"`
	AddressMode sourceengine.AddressMode `json:"addressMode"`
	WebURL      string                   `json:"webUrl"`
	Scanlator   string                   `json:"scanlator"`
}
