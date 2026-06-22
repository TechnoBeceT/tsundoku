package disk_test

import (
	"strings"
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// TestMarshalUnmarshalComicInfo verifies that a ComicInfo round-trips through
// Marshal → Unmarshal with all fields preserved, including provenance extensions.
func TestMarshalUnmarshalComicInfo(t *testing.T) {
	t.Parallel()

	original := disk.ComicInfo{
		Title:           "Test Chapter",
		Series:          "Test Series (Provider)",
		LocalizedSeries: "Test Series",
		Number:          "12.5",
		Count:           100,
		PageCount:       42,
		Format:          "Web",
		LanguageISO:     "en",
		Tags:            "Action,Adventure",
		AgeRating:       "Teen",
		Web:             "https://mangadex.org/chapter/abc",
		Writer:          "Author Name",
		Publisher:       "mangadex",
		Translator:      "dynasty",
		CoverArtist:     "Artist Name",
		Day:             15,
		Month:           3,
		Year:            2024,
		Manga:           "YesAndRightToLeft",
		Notes:           "Created by Tsundoku",
		// Provenance extensions
		Provider:   "mangadex",
		Scanlator:  "dynasty",
		Importance: 1,
		ChapterKey: "12.5",
	}

	data, err := disk.MarshalComicInfo(original)
	if err != nil {
		t.Fatalf("MarshalComicInfo: %v", err)
	}

	// Must start with the XML declaration.
	if !strings.HasPrefix(string(data), "<?xml") {
		t.Errorf("output does not begin with XML declaration")
	}

	got, err := disk.UnmarshalComicInfo(data)
	if err != nil {
		t.Fatalf("UnmarshalComicInfo: %v", err)
	}

	// Standard fields.
	assertEqual(t, "Title", original.Title, got.Title)
	assertEqual(t, "Series", original.Series, got.Series)
	assertEqual(t, "LocalizedSeries", original.LocalizedSeries, got.LocalizedSeries)
	assertEqual(t, "Number", original.Number, got.Number)
	assertEqual(t, "Count", original.Count, got.Count)
	assertEqual(t, "PageCount", original.PageCount, got.PageCount)
	assertEqual(t, "Format", original.Format, got.Format)
	assertEqual(t, "LanguageISO", original.LanguageISO, got.LanguageISO)
	assertEqual(t, "Tags", original.Tags, got.Tags)
	assertEqual(t, "Web", original.Web, got.Web)
	assertEqual(t, "Writer", original.Writer, got.Writer)
	assertEqual(t, "Publisher", original.Publisher, got.Publisher)
	assertEqual(t, "Translator", original.Translator, got.Translator)
	assertEqual(t, "CoverArtist", original.CoverArtist, got.CoverArtist)
	assertEqual(t, "Day", original.Day, got.Day)
	assertEqual(t, "Month", original.Month, got.Month)
	assertEqual(t, "Year", original.Year, got.Year)
	assertEqual(t, "Manga", original.Manga, got.Manga)
	assertEqual(t, "Notes", original.Notes, got.Notes)
	// Provenance extensions.
	assertEqual(t, "Provider", original.Provider, got.Provider)
	assertEqual(t, "Scanlator", original.Scanlator, got.Scanlator)
	assertEqual(t, "Importance", original.Importance, got.Importance)
	assertEqual(t, "ChapterKey", original.ChapterKey, got.ChapterKey)
}

// assertEqual is a typed comparison helper.
func assertEqual[T comparable](t *testing.T, field string, want, got T) {
	t.Helper()
	if want != got {
		t.Errorf("ComicInfo.%s: want %v, got %v", field, want, got)
	}
}
