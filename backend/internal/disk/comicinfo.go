package disk

import (
	"encoding/xml"
	"fmt"

	"github.com/technobecet/tsundoku/internal/chapter"
)

// ComicInfo is the ComicInfo.xml schema used by Komga and Kavita.
//
// Standard fields are taken from the ComicInfo 2.1 specification. The four
// provenance extensions (Provider, Scanlator, Importance, ChapterKey) are
// Tsundoku-specific additions stored as additional XML elements; Komga silently
// ignores unknown elements, so this is safe. They enable Task 7 to reconstruct
// the database from disk without any external index.
type ComicInfo struct {
	XMLName xml.Name `xml:"ComicInfo"`

	// Standard ComicInfo fields (Komga / Kavita specification).
	Title           string `xml:"Title,omitempty"`
	Series          string `xml:"Series,omitempty"`
	LocalizedSeries string `xml:"LocalizedSeries,omitempty"`
	Number          string `xml:"Number,omitempty"`
	Count           int    `xml:"Count,omitempty"`
	PageCount       int    `xml:"PageCount,omitempty"`
	Format          string `xml:"Format,omitempty"`
	LanguageISO     string `xml:"LanguageISO,omitempty"`
	Tags            string `xml:"Tags,omitempty"`
	AgeRating       string `xml:"AgeRating,omitempty"`
	Web             string `xml:"Web,omitempty"`
	Writer          string `xml:"Writer,omitempty"`
	Publisher       string `xml:"Publisher,omitempty"`
	Translator      string `xml:"Translator,omitempty"`
	CoverArtist     string `xml:"CoverArtist,omitempty"`
	Day             int    `xml:"Day,omitempty"`
	Month           int    `xml:"Month,omitempty"`
	Year            int    `xml:"Year,omitempty"`
	Manga           string `xml:"Manga,omitempty"`
	Notes           string `xml:"Notes,omitempty"`

	// Tsundoku provenance extensions — Komga ignores unknown XML elements.
	// These fields allow Task 7 to losslessly reconstruct the database from disk.

	// Provider is the source provider name (e.g. "mangadex").
	Provider string `xml:"Provider,omitempty"`

	// Scanlator is the scanlation group name.
	Scanlator string `xml:"Scanlator,omitempty"`

	// Importance is the provider importance rank (higher number = higher priority/quality; opposite of legacy Kaizoku.GO).
	// The value 0 serialises as absent (omitempty); the minimum real importance is 1.
	Importance int `xml:"Importance,omitempty"`

	// ChapterKey is the normalised chapter identity string produced by Task 1's
	// NormalizeChapterKey. Stored verbatim for lossless DB reconstruction.
	ChapterKey string `xml:"ChapterKey,omitempty"`
}

// MarshalComicInfo serialises a ComicInfo to indented XML bytes, prefixed by
// the standard XML declaration. The output is suitable for storage as
// ComicInfo.xml inside a CBZ archive.
func MarshalComicInfo(ci ComicInfo) ([]byte, error) {
	header := []byte(xml.Header)
	body, err := xml.MarshalIndent(ci, "", "  ")
	if err != nil {
		// Defensive path: xml.MarshalIndent on a ComicInfo struct (strings + ints only)
		// cannot fail in practice; this guard exists for future schema changes that add
		// channel/function fields.
		return nil, fmt.Errorf("disk.MarshalComicInfo: %w", err)
	}
	return append(header, body...), nil
}

// UnmarshalComicInfo parses ComicInfo XML bytes into a ComicInfo struct.
func UnmarshalComicInfo(data []byte) (*ComicInfo, error) {
	var ci ComicInfo
	if err := xml.Unmarshal(data, &ci); err != nil {
		return nil, fmt.Errorf("disk.UnmarshalComicInfo: %w", err)
	}
	return &ci, nil
}

// newComicInfo builds a ComicInfo from a RenderMeta and the resolved page count.
// It is the sole mapping from RenderMeta → ComicInfo and must be the only place
// that applies this transformation.
func newComicInfo(m RenderMeta, pageCount int) ComicInfo {
	chapName := m.ChapterName
	if chapName == "" && m.Number != nil {
		chapName = "Chapter " + chapter.FormatChapterNumber(*m.Number)
	}

	ci := ComicInfo{
		Title:           chapName,
		Series:          m.SeriesProviderTitle,
		LocalizedSeries: m.SeriesTitle,
		Format:          "Web",
		LanguageISO:     m.Language,
		PageCount:       pageCount,
		Writer:          m.Author,
		Publisher:       m.Provider,
		Translator:      m.Scanlator,
		CoverArtist:     m.Artist,
		Tags:            m.Tags,
		Notes:           "Created by Tsundoku",
		// Provenance extensions.
		Provider:   m.Provider,
		Scanlator:  m.Scanlator,
		Importance: m.Importance,
		ChapterKey: m.ChapterKey,
	}

	if m.Number != nil {
		ci.Number = chapter.FormatChapterNumber(*m.Number)
	}

	if m.ChapterCount > 0 {
		ci.Count = m.ChapterCount
	}

	// ci.Web is Komga's "read online" link — it must be the fully-qualified,
	// browser-clickable chapter URL (WebURL), NEVER the source-relative
	// addressing URL (m.URL is not even guaranteed to be an absolute URL for
	// every source). "" (WebURL unresolved) omits the field (omitempty).
	if m.WebURL != "" {
		ci.Web = m.WebURL
	}

	if m.UploadDate != nil {
		ci.Day = m.UploadDate.Day()
		ci.Month = int(m.UploadDate.Month())
		ci.Year = m.UploadDate.Year()
	}

	if m.Manga {
		ci.Manga = "YesAndRightToLeft"
	}

	return ci
}
