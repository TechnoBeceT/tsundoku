package disk

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/technobecet/tsundoku/internal/chapter"
)

// Category is the top-level library folder that groups series by publication type.
// Komga uses this as the first path component under the storage root.
type Category = string

// Recognised category values. Any other string is stored as-is.
const (
	CategoryManga  Category = "Manga"
	CategoryManhwa Category = "Manhwa"
	CategoryManhua Category = "Manhua"
	CategoryComic  Category = "Comic"
	CategoryOther  Category = "Other"
)

// RenderMeta carries all the metadata needed to generate a filename and
// ComicInfo for a single chapter render. It is shared by layout.go,
// comicinfo.go, and render.go.
type RenderMeta struct {
	// Provider is the source provider IDENTITY (e.g. the numeric Suwayomi source
	// ID). It is what downloads call Suwayomi with and what feeds the sidecar +
	// ComicInfo (Publisher + Provider extension) — reconcile reads it back, so it
	// MUST stay the stable ID, never the display name.
	Provider string

	// ProviderLabel is the human-readable provider name (e.g. "Comick") used for
	// the CBZ FILENAME token ONLY. When empty, GenerateCBZFilename falls back to
	// Provider (the ID) so rows with no resolved name still get a token. It never
	// feeds the sidecar or ComicInfo — those keep the ID for reconcile-safety.
	ProviderLabel string

	// Scanlator is the scanlation group name, if known.
	Scanlator string

	// Language is the BCP-47 language tag (e.g. "en").
	Language string

	// SeriesTitle is the display title of the series.
	SeriesTitle string

	// Category is the library category (Manga, Manhwa, etc.).
	Category Category

	// Number is the chapter number. Nil for un-numbered chapters.
	Number *float64

	// ChapterName is the chapter title from the provider.
	ChapterName string

	// MaxChapter is the highest known chapter number in the series, used for
	// integer-part zero-padding. Nil disables padding.
	MaxChapter *float64

	// Importance is the provider importance rank (higher number = higher priority/quality; opposite of legacy Kaizoku.GO).
	Importance int

	// ChapterKey is the normalised chapter identity string from Task 1.
	ChapterKey string

	// UploadDate is when the provider published this chapter.
	UploadDate *time.Time

	// URL is the provider-supplied canonical URL for this chapter.
	URL string

	// Author is the series author (for ComicInfo Writer field).
	Author string

	// Artist is the series artist (for ComicInfo CoverArtist field).
	Artist string

	// Tags is a comma-joined list of genre tags.
	Tags string

	// ChapterCount is the total chapter count known from the provider.
	ChapterCount int

	// SeriesProviderTitle is the provider's own title for the series
	// (used as ComicInfo.Series; SeriesTitle becomes LocalizedSeries).
	SeriesProviderTitle string

	// Manga when true sets ComicInfo.Manga = "YesAndRightToLeft".
	Manga bool
}

// GenerateCBZFilename produces the on-disk filename for a chapter archive.
//
// Format: [Provider-Scanlator][Language] SeriesTitle (ChapterName) ChapterNumber.cbz
//
// Rules:
//   - The provider token uses ProviderLabel (the human-readable source name),
//     falling back to Provider (the source ID) when ProviderLabel is empty.
//     Only the filename token switches to the name; the sidecar + ComicInfo keep
//     the ID (RenderMeta.Provider) untouched, so reconcile still round-trips.
//   - Hyphens in the provider token are replaced by underscores.
//   - Scanlator is appended with a hyphen when non-empty and distinct from the
//     provider LABEL.
//   - "[" and "]" in the provider string are converted to "(" and ")".
//   - Language tag is lowercased and omitted when empty.
//   - The chapter number is zero-padded in the integer part to match the width of
//     the maxChapter integer (e.g. chapter 5 with maxChapter 120 → "005").
//   - Decimal parts are kept as-is (e.g. 12.5 with maxChapter 120 → "012.5").
//   - ChapterName is included in parentheses unless isTitleChapter returns true.
//   - Parentheses inside ChapterName are converted to square brackets.
//   - Parentheses in the series title are stripped entirely.
//   - Invalid path characters in the result are replaced by Unicode lookalikes.
//   - Multiple consecutive spaces are collapsed to one.
//
// The returned name is byte-identical to the Kaizoku.GO output for the same inputs.
// chapter.FormatChapterNumber is reused from Task 1; do not duplicate it here.
func GenerateCBZFilename(m RenderMeta) string {
	// Filename token uses the display name; fall back to the ID when unresolved.
	label := m.ProviderLabel
	if label == "" {
		label = m.Provider
	}
	prov := buildProviderToken(label, m.Scanlator)

	lang := ""
	if m.Language != "" {
		lang = "[" + strings.ToLower(m.Language) + "]"
	}

	chapterStr := buildChapterStr(m.Number, m.MaxChapter)
	chapTitle := buildChapTitle(m.ChapterName)

	// Strip parentheses from the series title.
	title := strings.ReplaceAll(m.SeriesTitle, "(", "")
	title = strings.ReplaceAll(title, ")", "")

	name := fmt.Sprintf("[%s]%s %s%s %s", prov, lang, strings.TrimSpace(title), chapTitle, chapterStr)
	name = replaceInvalidPathCharacters(name)
	name = collapseSpaces(name)

	return name + ".cbz"
}

// buildProviderToken constructs the "[Label-Scanlator]" token. label is the
// resolved provider display name (or the ID fallback); scanlator is appended
// only when non-empty and distinct from the label.
func buildProviderToken(label, scanlator string) string {
	prov := strings.ReplaceAll(label, "-", "_")
	if scanlator != "" && scanlator != label {
		prov += "-" + scanlator
	}
	prov = strings.ReplaceAll(prov, "[", "(")
	prov = strings.ReplaceAll(prov, "]", ")")
	return prov
}

// buildChapterStr formats the chapter number with zero-padded integer part.
func buildChapterStr(number, maxChapter *float64) string {
	if number == nil {
		return ""
	}
	chapterStr := chapter.FormatChapterNumber(*number)
	if maxChapter == nil {
		return chapterStr
	}
	maxLen := len(fmt.Sprintf("%d", int(*maxChapter)))
	parts := strings.SplitN(chapterStr, ".", 2)
	for len(parts[0]) < maxLen {
		parts[0] = "0" + parts[0]
	}
	return strings.Join(parts, ".")
}

// buildChapTitle wraps a chapter name in parentheses, replacing inner parens
// with brackets. Returns an empty string when the name is a redundant title.
func buildChapTitle(chapterName string) string {
	if chapterName == "" {
		return ""
	}
	cleaned := strings.TrimSpace(chapterName)
	cleaned = strings.ReplaceAll(cleaned, "(", "[")
	cleaned = strings.ReplaceAll(cleaned, ")", "]")
	if isTitleChapter(cleaned) {
		return ""
	}
	return " (" + cleaned + ")"
}

// SeriesDir returns the absolute directory path for a series under the storage root.
//
// Layout: <storage>/<category>/<title>
// Both category and title are used verbatim — callers are responsible for
// sanitising the title before passing it in if needed.
func SeriesDir(storage, category, title string) string {
	return filepath.Join(storage, category, title)
}

// ChapterCBZPath returns the absolute path to a chapter's rendered CBZ file:
// <storage>/<category>/<title>/<filename>. It composes SeriesDir with the
// chapter's stored Filename so the disk layout contract lives in one place — the
// in-app reader and any future disk consumer resolve a chapter's archive
// identically, never re-deriving the path by hand.
func ChapterCBZPath(storage, category, title, filename string) string {
	return filepath.Join(SeriesDir(storage, category, title), filename)
}

// PageFilename returns the in-archive filename for a single page.
//
// It derives the base from GenerateCBZFilename (without the .cbz extension),
// appends a zero-padded page number (width = len(fmt.Sprint(maxPages))),
// and appends "." + ext.
//
// ext is a bare extension without a leading dot (e.g. "jpg", "png").
func PageFilename(m RenderMeta, pageNum, maxPages int, ext string) string {
	base := strings.TrimSuffix(GenerateCBZFilename(m), ".cbz")
	maxPadLen := len(fmt.Sprintf("%d", maxPages))
	pageStr := fmt.Sprintf("%0*d", maxPadLen, pageNum)
	return base + " " + pageStr + "." + ext
}
