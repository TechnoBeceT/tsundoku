package disk_test

import (
	"testing"

	"github.com/technobecet/tsundoku/internal/disk"
)

// ptr is a test helper that returns a pointer to a float64.
func ptr(f float64) *float64 { return &f }

// TestGenerateCBZFilename verifies byte-exact filename generation against
// hard-coded expected strings derived from the reference Kaizoku.GO assembly logic.
func TestGenerateCBZFilename(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		meta       disk.RenderMeta
		wantSuffix string // full expected filename
	}{
		{
			name: "simple integer chapter no scanlator no lang",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "",
				SeriesTitle: "Naruto",
				Number:      ptr(5),
				ChapterName: "",
				MaxChapter:  ptr(10),
			},
			// [mangadex] Naruto 05 — provider in brackets, no lang, zero-padded to width of "10"=2
			wantSuffix: "[mangadex] Naruto 05.cbz",
		},
		{
			name: "chapter with scanlator",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "dynasty",
				Language:    "en",
				SeriesTitle: "Attack on Titan",
				Number:      ptr(1),
				ChapterName: "",
				MaxChapter:  ptr(139),
			},
			// [mangadex-dynasty][en] Attack on Titan 001 — width of "139"=3
			wantSuffix: "[mangadex-dynasty][en] Attack on Titan 001.cbz",
		},
		{
			name: "chapter with title that is NOT redundant",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "One Piece",
				Number:      ptr(1000),
				ChapterName: "Straw Hat Luffy",
				MaxChapter:  ptr(1100),
			},
			// Width of "1100"=4 → "1000"
			wantSuffix: "[mangadex][en] One Piece (Straw Hat Luffy) 1000.cbz",
		},
		{
			name: "chapter name is redundant 'Chapter X' → dropped",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Berserk",
				Number:      ptr(5),
				ChapterName: "Chapter 5",
				MaxChapter:  ptr(5),
			},
			// "Chapter 5" is detected as redundant by isTitleChapter → dropped
			wantSuffix: "[mangadex][en] Berserk 5.cbz",
		},
		{
			name: "chapter name 'ch. X' is also redundant",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Vinland Saga",
				Number:      ptr(3),
				ChapterName: "ch. 3",
				MaxChapter:  ptr(10),
			},
			wantSuffix: "[mangadex][en] Vinland Saga 03.cbz",
		},
		{
			name: "decimal chapter number 12.5 with zero padding",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Dragon Ball",
				Number:      ptr(12.5),
				ChapterName: "",
				MaxChapter:  ptr(120),
			},
			// Width of "120"=3 → "012.5"
			wantSuffix: "[mangadex][en] Dragon Ball 012.5.cbz",
		},
		{
			name: "chapter 5 with maxChapter 120 → zero-padded to 3 digits",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "My Hero Academia",
				Number:      ptr(5),
				ChapterName: "",
				MaxChapter:  ptr(120),
			},
			wantSuffix: "[mangadex][en] My Hero Academia 005.cbz",
		},
		{
			name: "title with invalid path chars gets sanitized",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Sword Art Online: Alicization",
				Number:      ptr(1),
				ChapterName: "",
				MaxChapter:  ptr(9),
			},
			// The colon in the title gets replaced by the Armenian Full Stop ։
			wantSuffix: "[mangadex][en] Sword Art Online։ Alicization 1.cbz",
		},
		{
			name: "title with parentheses stripped",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Some (Title) Here",
				Number:      ptr(2),
				ChapterName: "",
				MaxChapter:  ptr(9),
			},
			// Parens in title are stripped
			wantSuffix: "[mangadex][en] Some Title Here 2.cbz",
		},
		{
			name: "provider with hyphen in name replaced by underscore",
			meta: disk.RenderMeta{
				Provider:    "manga-plus",
				Scanlator:   "",
				Language:    "ja",
				SeriesTitle: "Chainsaw Man",
				Number:      ptr(1),
				ChapterName: "",
				MaxChapter:  ptr(99),
			},
			// hyphens in provider replaced by underscore, no scanlator
			wantSuffix: "[manga_plus][ja] Chainsaw Man 01.cbz",
		},
		{
			name: "no chapter number",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "One Shot",
				Number:      nil,
				ChapterName: "The Only Chapter",
				MaxChapter:  nil,
			},
			// No number, name is not "Chapter X", so it appears in parens
			// With nil Number and nil MaxChapter, chapterStr is empty
			wantSuffix: "[mangadex][en] One Shot (The Only Chapter).cbz",
		},
		{
			name: "chapter name parens become brackets",
			meta: disk.RenderMeta{
				Provider:    "mangadex",
				Scanlator:   "",
				Language:    "en",
				SeriesTitle: "Blue Lock",
				Number:      ptr(100),
				ChapterName: "The (Final) Game",
				MaxChapter:  ptr(200),
			},
			// Parens in chapter name are replaced by brackets per the reference
			wantSuffix: "[mangadex][en] Blue Lock (The [Final] Game) 100.cbz",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := disk.GenerateCBZFilename(tc.meta)
			if got != tc.wantSuffix {
				t.Errorf("GenerateCBZFilename() = %q\n\t\t\twant %q", got, tc.wantSuffix)
			}
		})
	}
}

// TestSeriesDir verifies the storage/category/title directory layout.
func TestSeriesDir(t *testing.T) {
	t.Parallel()

	got := disk.SeriesDir("/library", "Manga", "Naruto")
	want := "/library/Manga/Naruto"
	if got != want {
		t.Errorf("SeriesDir() = %q, want %q", got, want)
	}
}

// TestPageFilename verifies zero-padded page filenames.
func TestPageFilename(t *testing.T) {
	t.Parallel()

	meta := disk.RenderMeta{
		Provider:    "mangadex",
		Scanlator:   "",
		Language:    "en",
		SeriesTitle: "Naruto",
		Number:      ptr(5),
		ChapterName: "",
		MaxChapter:  ptr(10),
	}
	// maxPages=20, pageNum=1 → "01"
	got := disk.PageFilename(meta, 1, 20, "jpg")
	// base is "[mangadex][en] Naruto 05" (no .cbz), then " 01.jpg"
	want := "[mangadex][en] Naruto 05 01.jpg"
	if got != want {
		t.Errorf("PageFilename() = %q, want %q", got, want)
	}
}
