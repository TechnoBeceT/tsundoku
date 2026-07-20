// Package series holds the library read service: listing and detail of the
// series that M2's ingest populates, with per-series chapter-state rollups.
// The ent predicate package internal/ent/series collides with this package name
// and must be imported aliased (entseries) wherever both are needed.
package series

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/technobecet/tsundoku/internal/category"
	"github.com/technobecet/tsundoku/internal/ent"
	"github.com/technobecet/tsundoku/internal/metadata"
	"github.com/technobecet/tsundoku/internal/pkg/chapterrange"
	"github.com/technobecet/tsundoku/internal/pkg/urlx"
)

// ChapterCounts is the per-series rollup of chapter download state used in list
// and detail responses. Total is every chapter; the other fields count chapters
// currently in that state. (States not broken out here — e.g. downloading,
// upgrading — still contribute to Total.)
type ChapterCounts struct {
	Total      int `json:"total"`
	Downloaded int `json:"downloaded"`
	Wanted     int `json:"wanted"`
	Failed     int `json:"failed"`
	// Unread = downloaded chapters the owner has not read. This is what can be
	// read RIGHT NOW — deliberately not "every chapter the source knows about",
	// which would read as noise on a partially-downloaded series.
	Unread int `json:"unread"`
}

// SeriesSummaryDTO is the list-row shape for a single series: identity,
// display metadata, the chapter-state rollup, and the monitoring flag.
// DisplayName is the resolved display title from the metadata source provider
// (falls back to the canonical Series.title). CoverURL is the series cover proxy
// path ("/api/series/{id}/cover"), empty when no provider has a cover_url.
type SeriesSummaryDTO struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	DisplayName string `json:"displayName"`
	Slug        string `json:"slug"`
	Category    string `json:"category"`
	CoverURL    string `json:"coverUrl"`
	Monitored   bool   `json:"monitored"`
	Completed   bool   `json:"completed"`
	// NeedsSource is true when the series has ≥1 dangling (disk-origin,
	// unlinked) provider — files not backed by a live source — OR no providers
	// at all, EVEN IF another provider is already matched/live (see
	// needsSource). It is COVER-INDEPENDENT: a Kaizoku-migration series can
	// carry a metadata cover (via AutoIdentify/SetCover) while still having a
	// source gap, and the owner needs that fact visible regardless of whether a
	// cover renders (handover 2026-07-13#15).
	NeedsSource   bool          `json:"needsSource"`
	ChapterCounts ChapterCounts `json:"chapterCounts"`
	// CreatedAt is when the series entered the library (RFC3339). Powers the
	// "recently added" sort. Always present.
	CreatedAt string `json:"createdAt"`
	// LastChapterDownloadedAt is when this series' NEWEST chapter became READABLE
	// — MAX(first_downloaded_at) (RFC3339; null when no chapter ever carried one).
	// Powers "recently updated".
	//
	// NOT MAX(download_date): that is a FETCH timestamp, and a convergence upgrade
	// rewrites it on an OLD chapter — which would float a series to the top with
	// nothing new to read. A nil value serializes as JSON null, never the zero
	// time and never "".
	LastChapterDownloadedAt *string `json:"lastChapterDownloadedAt"`
	// LatestChapterAt is when this series' newest chapter was RELEASED — the
	// series-bound MAX(effectiveReleaseDate) across ANY provider (the source's
	// provider_upload_date, else a chapter's download_date fallback). It answers
	// "when did I last get a new chapter" and powers the longest-waiting /
	// recently-updated sort. RFC3339; null when no chapter carries any date.
	// Distinct from LastChapterDownloadedAt (a FETCH timestamp): this tracks the
	// SOURCE's publish date, which is what "am I waiting" is really measured on.
	LatestChapterAt *string `json:"latestChapterAt"`
	// IsStalled is true when LatestChapterAt is older than the stalled threshold
	// (health.stalled_threshold_days, default 30) AND the series is still monitored
	// AND not completed — i.e. the owner is waiting and no source has published in
	// the window. SERIES-BOUND: a series with one dead source but another still
	// publishing is NOT stalled. Purely informational — nothing auto-drops.
	IsStalled bool `json:"isStalled"`
}

// SeriesDetailDTO is the full series view: the summary fields plus the series'
// chapters (ordered by number then chapter_key), its providers, and the
// monitoring flag. DisplayName and CoverURL follow the same resolution as
// SeriesSummaryDTO.
//
// The rich-metadata fields (Status/Genres/Tags/AltTitles/Authors/Year/Links/
// MetadataSource/CoverSource) are populated by the Phase-1 native metadata
// engine (internal/metadatasvc) — see spec/metadata-engine-phase1 §3/§5. They
// are DETAIL-ONLY (never surfaced on SeriesSummaryDTO — the library grid does
// not need a genre list per row) and are the zero value (""/0/nil→[]) on a
// series that has never been identified against a metadata provider.
type SeriesDetailDTO struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	DisplayName string `json:"displayName"`
	Slug        string `json:"slug"`
	Category    string `json:"category"`
	CoverURL    string `json:"coverUrl"`
	Monitored   bool   `json:"monitored"`
	Completed   bool   `json:"completed"`
	// NeedsSource mirrors SeriesSummaryDTO.NeedsSource — see its doc comment.
	NeedsSource   bool          `json:"needsSource"`
	ChapterCounts ChapterCounts `json:"chapterCounts"`
	// CreatedAt / LastChapterDownloadedAt are the library-grid sort keys, carried
	// here too so detailToSummary (the detail→summary projection every mutating
	// handler uses) never drops them (§16). See the SeriesSummaryDTO fields for
	// the semantics — LastChapterDownloadedAt is MAX(first_downloaded_at), NOT
	// MAX(download_date).
	CreatedAt               string  `json:"createdAt"`
	LastChapterDownloadedAt *string `json:"lastChapterDownloadedAt"`
	// LatestChapterAt / IsStalled mirror SeriesSummaryDTO (see its field docs) —
	// carried here too so detailToSummary never drops them (§16).
	LatestChapterAt *string       `json:"latestChapterAt"`
	IsStalled       bool          `json:"isStalled"`
	Chapters        []ChapterDTO  `json:"chapters"`
	Providers       []ProviderDTO `json:"providers"`

	// Status is the metadata-engine normalized publication status
	// ("ongoing"|"completed"|"hiatus"|"cancelled"|""). Distinct from Completed
	// above, which is the owner's own manual toggle — Status is descriptive
	// (what the metadata provider reports), Completed is prescriptive (what the
	// owner decided the refresh/health sweep should do).
	Status string `json:"status"`
	// Description is the metadata-engine merged synopsis (see
	// internal/metadatasvc's persist step); "" on a series that has never been
	// identified against a metadata provider.
	Description string `json:"description"`
	// Genres/Tags are the merged metadata-engine collections (union across every
	// matched provider — see metadata.Merge). Always non-nil so the JSON is []
	// rather than null on an unidentified series.
	Genres []string `json:"genres"`
	Tags   []string `json:"tags"`
	// AltTitles/Authors/Links mirror the merged metadata-engine collections.
	AltTitles []AltTitleDTO `json:"altTitles"`
	Authors   []AuthorDTO   `json:"authors"`
	Links     []LinkDTO     `json:"links"`
	// Year is the first-publication year merged from the metadata engine; 0 =
	// unknown/unidentified.
	Year int `json:"year"`
	// MetadataSource/CoverSource are the provenance descriptors for the merged
	// rich metadata and the chosen cover, respectively (they are independent —
	// QCAT-228 — a cover pick never implies a metadata re-merge and vice versa).
	// Both are nil on a series that has never been identified/never had a cover
	// explicitly chosen via the metadata engine.
	MetadataSource *SourceRefDTO `json:"metadataSource"`
	CoverSource    *SourceRefDTO `json:"coverSource"`
	// MetadataLocked is true once the owner has hand-curated this series'
	// rich metadata via a manual Identify/IdentifyMerge pick — the Phase-1
	// metadata engine's background AutoIdentify pass never overwrites a
	// locked series (see metadatasvc.Service.AutoIdentify's guard). False on
	// a series that has never been manually identified (AutoIdentify may
	// still populate it).
	MetadataLocked bool `json:"metadataLocked"`
}

// AltTitleDTO mirrors metadata.AltTitle for the wire (camelCase JSON).
type AltTitleDTO struct {
	Name string `json:"name"`
	// Type is one of ROMAJI, LOCALIZED, NATIVE, SYNONYM.
	Type string `json:"type"`
	Lang string `json:"lang"`
}

// AuthorDTO mirrors metadata.Author for the wire (camelCase JSON).
type AuthorDTO struct {
	Name string `json:"name"`
	// Role is one of WRITER, ARTIST, STORY, ART, ... (provider-defined).
	Role string `json:"role"`
}

// LinkDTO mirrors metadata.Link for the wire (camelCase JSON).
type LinkDTO struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// SourceRefDTO mirrors metadata.SourceRef for the wire (camelCase JSON) — the
// provenance descriptor for a series' merged metadata or chosen cover.
type SourceRefDTO struct {
	// Kind is "metadata" (v1) | "source" | "tracker" (later).
	Kind string `json:"kind"`
	// Ref is the provider Key() | SeriesProvider UUID | tracker id.
	Ref       string `json:"ref"`
	RemoteID  string `json:"remoteId"`
	RemoteURL string `json:"remoteUrl"`
}

// mapAltTitles maps the Series row's stored AltTitles into their DTO form.
// Always returns a non-nil slice so the JSON renders [] rather than null on an
// unidentified series.
func mapAltTitles(alts []metadata.AltTitle) []AltTitleDTO {
	out := make([]AltTitleDTO, 0, len(alts))
	for _, a := range alts {
		out = append(out, AltTitleDTO{Name: a.Name, Type: a.Type, Lang: a.Lang})
	}
	return out
}

// mapAuthors maps the Series row's stored Authors into their DTO form. Always
// returns a non-nil slice (see mapAltTitles).
func mapAuthors(authors []metadata.Author) []AuthorDTO {
	out := make([]AuthorDTO, 0, len(authors))
	for _, a := range authors {
		out = append(out, AuthorDTO{Name: a.Name, Role: a.Role})
	}
	return out
}

// mapLinks maps the Series row's stored Links into their DTO form. Always
// returns a non-nil slice (see mapAltTitles).
func mapLinks(links []metadata.Link) []LinkDTO {
	out := make([]LinkDTO, 0, len(links))
	for _, l := range links {
		out = append(out, LinkDTO{Label: l.Label, URL: l.URL})
	}
	return out
}

// sourceLinks appends one LinkDTO per real-source SeriesProvider onto the
// metadata-engine merged links, so the rich card's LINKS row surfaces the
// library's actual sources (Asura, Comix, …) alongside MAL/AniList/MangaUpdates/
// etc. The label reuses ProviderLabel (provider_name, falling back to the raw
// provider id) — the same label the providers list already shows — NOT
// SeriesProvider.Title, which is that source's per-manga title (usually just
// the series title again) and would make a poor link label.
//
// The link PREFERS the provider's WebURL — the fully-qualified,
// browser-clickable page (the source's realUrl) — and FALLS BACK to the
// addressing URL, which for many sources is ITSELF an absolute browser URL
// (e.g. Asura's "https://asura.example/manga/.."). Only a genuinely
// source-relative addressing URL (e.g. "/605z7-teach-me-first") is not a
// working link, and the FE LinkChip scheme-guards THAT case into an
// inert/greyed pill rather than a broken link. (WebURL-only was wrong: a source
// whose addressing URL is already absolute would render a greyed dead pill even
// though a perfectly good link exists — the source-link end-to-end test pins
// this.) A provider with no addressing URL at all (a disk-origin/unlinked row —
// no real source, nothing to link to) contributes nothing.
//
// DEDUP: a link URL that already appears among existing (case-insensitive exact
// match) links is skipped, so a source a metadata provider also lists isn't
// doubled. providers must be the series' already-eager-loaded
// Edges.Providers (every caller — GetSeries — loads it for ProviderDTO already),
// so this adds zero extra queries. existing is returned untouched (still
// non-nil) when no provider qualifies.
func sourceLinks(providers []*ent.SeriesProvider, existing []LinkDTO) []LinkDTO {
	seen := make(map[string]struct{}, len(existing))
	for _, l := range existing {
		seen[strings.ToLower(l.URL)] = struct{}{}
	}

	out := existing
	for _, p := range providers {
		// No addressing URL ⇒ a disk-origin/unlinked row with no real source.
		if p.URL == "" {
			continue
		}
		// Prefer the fully-qualified browser WebURL (realUrl); fall back to the
		// addressing URL ONLY when it is itself an absolute http(s) URL (many
		// sources store a full browser URL there, e.g. Asura). A source-relative
		// addressing URL (e.g. "/delta") is NOT a working link, so it stays an
		// empty (greyed) pill — never emitted as an href.
		linkURL := p.WebURL
		if linkURL == "" && urlx.IsAbsoluteHTTP(p.URL) {
			linkURL = p.URL
		}
		// Dedup a resolved link URL — empty (greyed) ones never merge, so several
		// unresolved sources can coexist.
		if linkURL != "" {
			key := strings.ToLower(linkURL)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, LinkDTO{Label: ProviderLabel(p), URL: linkURL})
	}
	return out
}

// mapSourceRef maps a nullable Series.metadata_source/cover_source column into
// its DTO form. nil stays nil (JSON null) — the field is genuinely absent
// until the series is identified or a cover is explicitly chosen.
func mapSourceRef(ref *metadata.SourceRef) *SourceRefDTO {
	if ref == nil {
		return nil
	}
	return &SourceRefDTO{Kind: ref.Kind, Ref: ref.Ref, RemoteID: ref.RemoteID, RemoteURL: ref.RemoteURL}
}

// nonNilStrings returns s unchanged when non-nil, else an empty (non-nil)
// slice — Series.Genres/Tags are Optional jsonb columns and read back nil when
// never set, but the DTO contract is "always [] never null" (mirrors
// FractionalChapters/Links/etc above).
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ChapterDTO is one chapter in a series-detail response. ID is the Chapter UUID —
// the identifier the in-app reader's page-bytes (GET …/chapters/{chapterId}/pages/{n})
// and progress (PATCH /api/chapters/{id}/progress) endpoints key on, so the client
// can open the reader straight from a chapter row. Number is the display/
// sort value (nullable — never identity, that is ChapterKey). PageCount is
// nullable until the chapter is downloaded (nil = unknown). Read and LastReadPage
// carry the in-app reader's owner progress (Read defaults false, LastReadPage 0);
// the reader uses PageCount + LastReadPage to resume mid-chapter. ReadAt is the
// timestamp of the most recent read=true transition (nil until read, cleared on
// read=false — see newChapterDTO). PageVersion is the reader's page-bytes cache
// buster (see PageVersion in reader.go); "" for a not-yet-downloaded chapter. The
// client appends it as ?v= on every page request so a Library-Convergence upgrade
// that replaces the CBZ mid-read is never served from a stale cache entry.
type ChapterDTO struct {
	ID           string   `json:"id"`
	ChapterKey   string   `json:"chapterKey"`
	Number       *float64 `json:"number"`
	Name         string   `json:"name"`
	State        string   `json:"state"`
	Filename     string   `json:"filename"`
	PageCount    *int     `json:"pageCount"`
	Read         bool     `json:"read"`
	LastReadPage int      `json:"lastReadPage"`
	// ReadAt is when the owner marked this chapter read; nil until then (and
	// cleared when read flips back to false).
	ReadAt      *time.Time `json:"readAt"`
	PageVersion string     `json:"pageVersion"`
	// ReleaseDate is this chapter's effective release date (QCAT-297,
	// Komikku-style): the satisfying/best provider's provider_upload_date for the
	// chapter's key, else the chapter's download_date. Null only for a chapter no
	// source dated that was never downloaded. Rendered under each chapter row.
	ReleaseDate *time.Time `json:"releaseDate"`
}

// ProviderDTO is one SeriesProvider in a series-detail response. ID is the
// SeriesProvider UUID (used by re-rank). Provider is the raw Suwayomi source-ID
// identity key; ProviderName is its human-readable display label (falls back to
// the id when no name was captured) — the UI shows ProviderName, keeps Provider
// for identity. Importance is the priority/quality rank
// (higher = preferred). Title is this provider's own title for the series.
// CoverURL is the provider-level cover proxy path
// ("/api/series/{sid}/providers/{pid}/cover"). IsMetadataSource is true for the
// resolved metadata provider (the one currently supplying DisplayName + CoverURL).
// The health fields (Health, ChaptersBehind, NewestChapterAt, LastSyncedAt,
// LastError) are derived on read — never persisted. Linked is false for a
// disk-origin provider (fails IsLinkedProvider — an "unlinked/unknown group"
// created by library import/reconcile, never a real live source) so the FE
// can list it as a Match candidate. MangaID is ALWAYS 0 (P2 Suwayomi-removal:
// the url-addressed engine host has no per-manga numeric id equivalent to
// the retired SuwayomiID column) — retained, not read as meaningful, purely
// for FE wire compatibility (mirrors the same "unused, kept for wire compat"
// carve-out already made for AdoptProvider/ProviderRef.MangaID elsewhere in
// this P2 migration).
//
// The two chapter numbers on this DTO answer DIFFERENT questions and must never
// be confused in the UI:
//   - ChapterCount is how many of the series' chapters this provider currently
//     SATISFIES (Chapter.satisfied_by_provider_id == this provider) — i.e. how
//     many of the owner's downloaded files came from here.
//   - FeedCount / FeedRanges are what this provider OFFERS: the size and the
//     gap-collapsed span ("1-269") of its stored ProviderChapter feed. Because
//     ingest filters the feed by scanlator, a (source, scanlator) provider's feed
//     is exactly that pair's true offering. Both are read straight off the
//     already-eager-loaded feed rows — surfacing them costs ZERO extra DB queries
//     and, crucially, ZERO calls to the source (the owner used to have to trigger
//     a live per-source breakdown fetch to see a number we already hold).
type ProviderDTO struct {
	ID               string `json:"id"`
	Provider         string `json:"provider"`
	ProviderName     string `json:"providerName"`
	Title            string `json:"title"`
	CoverURL         string `json:"coverUrl"`
	IsMetadataSource bool   `json:"isMetadataSource"`
	Linked           bool   `json:"linked"`
	MangaID          int    `json:"mangaId"`
	ChapterCount     int    `json:"chapterCount"`
	// FeedCount is how many chapters this provider OFFERS (its stored
	// ProviderChapter feed size) — 0 for a provider with no feed.
	FeedCount int `json:"feedCount"`
	// FeedRanges is that feed's coverage as a gap-collapsed display string
	// ("1-90, 92-101"); "" when the feed is empty or carries no chapter numbers.
	FeedRanges string `json:"feedRanges"`
	// HasFeed is true when this provider has a non-empty availability feed
	// (≥1 ProviderChapter). Mirrors the backend drift-merge feed gate so the FE
	// offers exactly the pairs the backend would merge.
	HasFeed bool `json:"hasFeed"`
	// FractionalCount / FractionalChapters expose the fractional-numbered chapters
	// (5.1, 5.5 …) in THIS provider's stored feed. They are the EVIDENCE the owner
	// needs before ticking IgnoreFractional: a mirror that re-uploads whole chapters
	// under an "N.1" suffix shows a long SYSTEMATIC run (1.1, 2.1, 3.1, …), while a
	// source carrying a genuine side-chapter shows a lone 5.5. The engine cannot
	// tell those apart — the owner can, but only if he can SEE them.
	//
	// Read from p.Edges.ProviderChapters, which every caller already eager-loads —
	// no extra query and no source call (identical to FeedCount/FeedRanges).
	// FractionalChapters is ascending and always non-nil, so the JSON renders [],
	// never null.
	FractionalCount    int      `json:"fractionalCount"`
	FractionalChapters []string `json:"fractionalChapters"`
	// IgnoreFractional is the owner's per-(series, source) switch marking this
	// source as a fractional re-uploader: when set it contributes no
	// fractional-numbered chapters to this series. It is reported here even when
	// set — an ignored source keeps showing its fractional list, so the owner can
	// always review what he suppressed and un-tick it.
	IgnoreFractional bool       `json:"ignoreFractional"`
	Scanlator        string     `json:"scanlator"`
	Language         string     `json:"language"`
	Importance       int        `json:"importance"`
	Health           string     `json:"health"`
	ChaptersBehind   int        `json:"chaptersBehind"`
	NewestChapterAt  *time.Time `json:"newestChapterAt"`
	LastSyncedAt     *time.Time `json:"lastSyncedAt"`
	LastError        string     `json:"lastError"`
}

// LibraryHealthDTO is the library-wide source-health scan: only series that
// have at least one stale or erroring source.
type LibraryHealthDTO struct {
	Series []SeriesHealthDTO `json:"series"`
}

// SeriesHealthDTO is one sick series in the library-health scan and its sick
// sources.
type SeriesHealthDTO struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Slug    string        `json:"slug"`
	Sources []ProviderDTO `json:"sources"`
}

// newSummaryDTO maps an ent.Series plus its computed rollup into a summary DTO.
// s.Edges.Providers AND s.Edges.Category must be eagerly loaded; MetadataProvider
// + SeriesDisplay resolve DisplayName and CoverURL from the provider set, and
// category.NameOf resolves the category name from the edge. rollup carries the
// chapter-state counts and the newest first_downloaded_at (nil when unknown).
func newSummaryDTO(s *ent.Series, rollup seriesRollup, latestChapterAt *time.Time, isStalled bool) SeriesSummaryDTO {
	meta := MetadataProvider(s)
	dispName, coverURL := SeriesDisplay(s, meta)
	return SeriesSummaryDTO{
		ID:                      s.ID.String(),
		Title:                   s.Title,
		DisplayName:             dispName,
		Slug:                    s.Slug,
		Category:                category.NameOf(s),
		CoverURL:                coverURL,
		Monitored:               s.Monitored,
		Completed:               s.Completed,
		NeedsSource:             needsSource(s.Edges.Providers),
		ChapterCounts:           rollup.Counts,
		CreatedAt:               formatRFC3339(s.CreatedAt),
		LastChapterDownloadedAt: formatRFC3339Ptr(rollup.LastChapterDownloadedAt),
		LatestChapterAt:         formatRFC3339Ptr(latestChapterAt),
		IsStalled:               isStalled,
	}
}

// needsSource is true when the series has ≥1 DANGLING provider — a disk-origin,
// unlinked row (fails IsLinkedProvider: created by library import/reconcile/the
// Kaizoku migration, its files not backed by a real engine-host source) — OR no
// providers at all. Crucially this holds EVEN WHEN the series already has a live
// source: a partially-consolidated series (the exact kaliscan mid-migration
// state — some domains matched, one still dangling) must surface so the owner
// can find it and finish consolidating (QCAT-295 Part C; the old rule "NONE of
// the providers is live" hid these and made them unfindable). Cover state is
// deliberately irrelevant (handover 2026-07-13#15): a series can carry a
// metadata cover via AutoIdentify/SetCover while still having a source gap —
// "needs source" tracks that gap independently, regardless of download/
// completion state. Zero-provider series keep needing one.
func needsSource(providers []*ent.SeriesProvider) bool {
	if len(providers) == 0 {
		return true
	}
	for _, p := range providers {
		if !IsLinkedProvider(p) {
			return true
		}
	}
	return false
}

// formatRFC3339 renders a timestamp as a UTC RFC3339 string — the wire form for
// the summary/detail sort-key fields.
func formatRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// formatRFC3339Ptr renders a nullable timestamp: nil stays nil (JSON null),
// never the zero time and never "", so a series with no readable chapter can
// never sort as "the beginning of time".
func formatRFC3339Ptr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatRFC3339(*t)
	return &s
}

// newChapterDTO maps an ent.Chapter into its detail DTO. The chapter's display
// title lives on the provider feed, not the Chapter row, so the resolved name
// (best-provider ProviderChapter.name) is passed in by the caller. When no
// provider supplies a title we fall back to "Chapter N" derived from the chapter
// number — a frozen 0-provider series (all sources removed via M6) keeps its CBZs
// and Chapter rows but loses the title source, so the number is the only display
// name left. If even the number is absent (a rare corner) the name stays blank.
func newChapterDTO(c *ent.Chapter, name string, releaseDate *time.Time) ChapterDTO {
	return ChapterDTO{
		ID:           c.ID.String(),
		ChapterKey:   c.ChapterKey,
		Number:       c.Number,
		Name:         chapterDisplayName(name, c.Number),
		State:        c.State.String(),
		Filename:     c.Filename,
		PageCount:    c.PageCount,
		Read:         c.Read,
		LastReadPage: c.LastReadPage,
		ReadAt:       c.ReadAt,
		PageVersion:  PageVersion(c.Filename, c.DownloadDate),
		ReleaseDate:  releaseDate,
	}
}

// chapterDisplayName returns the chapter's display name: the provider-resolved
// title if present, else "Chapter N" from number (minimally formatted so 12.0 →
// "Chapter 12" and 12.5 → "Chapter 12.5"), else "" when there is no number.
func chapterDisplayName(name string, number *float64) string {
	if name != "" {
		return name
	}
	if number != nil {
		return "Chapter " + strconv.FormatFloat(*number, 'f', -1, 64)
	}
	return ""
}

// newProviderDTO maps an ent.SeriesProvider and its computed health into a
// detail DTO. seriesID and isMetadataSource are passed in by the caller after
// resolving the series' metadata provider once for the whole provider slice.
// CoverURL is the provider-level proxy path when the provider has a non-empty
// cover_url, else "" (mirroring the series-level SeriesDisplay behaviour so the
// SPA never fires a cover fetch that would 404). Title is the provider's own
// title for the series (set at ingest, may be "").
//
// FeedCount/FeedRanges/HasFeed and the FractionalCount/FractionalChapters pair are
// all read from p.Edges.ProviderChapters, which every caller already eager-loads
// (GetSeries / loadSeriesWithHealthData) — no extra query, no source call.
func newProviderDTO(p *ent.SeriesProvider, h ProviderHealth, seriesID uuid.UUID, isMetadataSource bool, chapterCount int) ProviderDTO {
	var coverURL string
	if p.CoverURL != "" {
		coverURL = "/api/series/" + seriesID.String() + "/providers/" + p.ID.String() + "/cover"
	}
	fracCount, fracChapters := fractionalFeed(p)
	return ProviderDTO{
		ID:               p.ID.String(),
		Provider:         p.Provider,
		ProviderName:     ProviderLabel(p),
		Title:            p.Title,
		CoverURL:         coverURL,
		IsMetadataSource: isMetadataSource,
		Linked:           IsLinkedProvider(p),
		// MangaID is always 0 now — see the ProviderDTO doc comment.
		MangaID:      0,
		ChapterCount: chapterCount,
		FeedCount:    len(p.Edges.ProviderChapters),
		FeedRanges:   feedRanges(p),
		HasFeed:      len(p.Edges.ProviderChapters) > 0,

		FractionalCount:    fracCount,
		FractionalChapters: fracChapters,
		IgnoreFractional:   p.IgnoreFractional,

		Scanlator:       p.Scanlator,
		Language:        p.Language,
		Importance:      p.Importance,
		Health:          h.Status,
		ChaptersBehind:  h.ChaptersBehind,
		NewestChapterAt: h.NewestChapterAt,
		LastSyncedAt:    h.LastSyncedAt,
		LastError:       h.LastError,
	}
}

// feedRanges renders one provider's STORED feed (p.Edges.ProviderChapters — the
// caller must have eager-loaded it) as a gap-collapsed coverage string, e.g.
// "1-90, 92-101". Only feed rows carrying a chapter number contribute; a feed
// that is empty (or wholly number-less) yields "" — never a bogus "0-0".
// Purely in-memory: no DB query and, deliberately, no call to the source.
func feedRanges(p *ent.SeriesProvider) string {
	numbers := make([]float64, 0, len(p.Edges.ProviderChapters))
	for _, pc := range p.Edges.ProviderChapters {
		if pc.Number != nil {
			numbers = append(numbers, *pc.Number)
		}
	}
	return chapterrange.FormatChapterRanges(numbers)
}

// fractionalFeed lists the fractional-numbered chapters in one provider's STORED
// feed (p.Edges.ProviderChapters — the caller must have eager-loaded it), ascending,
// as display strings ("1.1", "5.5"), and returns how many there are. "Fractional"
// is chapterrange.IsFractional — the ONE definition in the codebase, shared with the
// supersede engine and the ingest/candidacy gates, so this view can never drift from
// what the engine actually suppresses.
//
// Purely in-memory: no DB query and, deliberately, no call to the source — the feed
// rows are already loaded (this is what makes FeedCount/FeedRanges free too). A feed
// row with no chapter number contributes nothing. The returned slice is never nil,
// so the JSON renders [] rather than null.
func fractionalFeed(p *ent.SeriesProvider) (int, []string) {
	numbers := make([]float64, 0, len(p.Edges.ProviderChapters))
	for _, pc := range p.Edges.ProviderChapters {
		if pc.Number != nil && chapterrange.IsFractional(*pc.Number) {
			numbers = append(numbers, *pc.Number)
		}
	}
	slices.Sort(numbers)

	out := make([]string, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, strconv.FormatFloat(n, 'f', -1, 64))
	}
	return len(out), out
}

// providerChapterCounts tallies, for one loaded series, how many chapters each
// provider currently satisfies (Chapter.satisfied_by_provider_id). row must
// have its Chapters edge eagerly loaded (GetSeries / loadSeriesWithHealthData
// both do) — this is an in-memory rollup, no extra query (no N+1).
func providerChapterCounts(row *ent.Series) map[uuid.UUID]int {
	counts := make(map[uuid.UUID]int, len(row.Edges.Providers))
	for _, ch := range row.Edges.Chapters {
		if ch.SatisfiedByProviderID == nil {
			continue
		}
		counts[*ch.SatisfiedByProviderID]++
	}
	return counts
}
