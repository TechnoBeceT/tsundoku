/**
 * Prop/data types for the Series Detail screen (`GET /api/series/:id`).
 *
 * `SeriesDetail` is everything `SeriesSummary` carries (re-used from the shared
 * `screens/types.ts`) PLUS the full chapter and provider feeds. Like the other
 * screen types these are hand-light + presentation-only — the screen receives
 * everything via props and never imports the generated API client.
 */
import type { SeriesSummary } from './types'

/** The eight download states a chapter moves through (the backend state machine). */
export type ChapterState =
  | 'wanted'
  | 'downloading'
  | 'downloaded'
  | 'upgrade_available'
  | 'upgrading'
  | 'failed'
  | 'permanently_failed'
  | 'superseded'

/** Per-provider health: current, gone stale, or erroring on last refresh. */
export type ProviderHealth = 'ok' | 'stale' | 'erroring'

/**
 * Chapter — one row in the chapter table. Identity is `chapterKey` (never the
 * `number`, which may be null and is display/sort only). `filename` is non-empty
 * only once the CBZ is on disk; `pageCount` is null until then.
 */
export interface Chapter {
  /** Chapter UUID — the identifier the reader's page/progress endpoints key on. */
  id: string
  /** Stable identity (NOT the number). */
  chapterKey: string
  /** Display/sort number (e.g. 1, 1.5); null when unknown. */
  number: number | null
  /** Resolved display name from the best provider; may be empty. */
  name: string
  /** Current download state. */
  state: ChapterState
  /** Rendered CBZ filename; empty until downloaded. */
  filename: string
  /** Page count; null until downloaded. */
  pageCount: number | null
  /** In-app reader progress: true once the owner has marked the chapter fully read. */
  read: boolean
  /** In-app reader progress: 0-based index of the last page the owner viewed. */
  lastReadPage: number
  /** ISO timestamp the chapter was marked read; null until then (cleared when `read` flips back to false). */
  readAt: string | null
}

/**
 * Provider — one tracked source for a series. Sources are ranked by `importance`
 * (higher integer = preferred); the highest is the authoritative chapter-title
 * source. Health/sync fields drive the per-source status line.
 */
export interface Provider {
  /** SeriesProvider UUID (used to reorder or remove this source). */
  id: string
  /** Raw Suwayomi source-ID identity key (e.g. `7537715367149829912`). */
  provider: string
  /** Human-readable source display name (e.g. `WebToon`); falls back to the id upstream. Shown in place of the id. */
  providerName: string
  /**
   * Source's Suwayomi manga id; `0` = unlinked disk-origin provider (no real
   * source attached).
   */
  mangaId: number
  /**
   * False for a disk-origin provider — an unlinked/unknown group created by
   * library import with no real Suwayomi source attached yet (`suwayomi_id=0`
   * on the backend); true once a real source is attached via adopt,
   * add-source, or Match. Unlinked groups are Match candidates (the "Match to
   * source" row action → `MatchDiskProviderDialog`).
   */
  linked: boolean
  /**
   * How many of the series' chapters this provider currently SUPPLIES — i.e.
   * how many downloaded files came from here. NOT the source's offering: that
   * is `feedCount`. (Showing this as a bare "N chapters" is the bug this pair
   * of fields fixes.)
   */
  chapterCount: number
  /**
   * How many chapters this source OFFERS — the size of its stored
   * ProviderChapter feed, scanlator-filtered, so for a (source, scanlator)
   * provider it is that pair's true offering. Rides the series-detail response:
   * no extra request and NO live call to the source.
   */
  feedCount: number
  /** That stored feed's gap-collapsed coverage, e.g. `"1-90, 92-101"`; empty when the feed is empty. */
  feedRanges: string
  /** True when this provider has a non-empty availability feed (≥1 ProviderChapter) — the exact backend drift-merge gate. */
  hasFeed: boolean
  /**
   * How many chapters in this source's stored feed carry a FRACTIONAL number
   * (5.1, 5.5 — i.e. `number != floor(number)`). Rides the series-detail
   * response like `feedCount`: no extra request, no call to the source.
   */
  fractionalCount: number
  /**
   * Those fractional chapter numbers, ascending, as display strings
   * (`["1.1", "2.1"]`); always an array, `[]` when there are none.
   *
   * This is the EVIDENCE the owner judges `ignoreFractional` from, and it must
   * be rendered, never hidden: a mirror that re-uploads whole chapters under an
   * "N.1" suffix shows a long SYSTEMATIC run (1.1, 2.1, 3.1, …), while a source
   * carrying a genuine side-chapter (omake) shows a lone 5.5. No heuristic can
   * tell those apart — the owner decides from the list.
   */
  fractionalChapters: string[]
  /**
   * The owner's per-(series, source) switch marking this source as a fractional
   * re-uploader: when set, the source contributes no fractional-numbered
   * chapters to this series (dropped at ingest, excluded from candidacy).
   * It DELETES NOTHING — downloaded files and existing chapters are kept, and
   * un-ticking restores the source immediately.
   */
  ignoreFractional: boolean
  /** Scanlation group name (may be empty → row omits it). */
  scanlator: string
  /** BCP-47 language code (e.g. `en`, `ko`). */
  language: string
  /** Priority rank — higher number = preferred. */
  importance: number
  /** Source health badge value. */
  health: ProviderHealth
  /** How many of the series' chapters this source lacks. */
  chaptersBehind: number
  /** ISO timestamp this source last posted a new chapter; null = unknown. */
  newestChapterAt: string | null
  /** ISO timestamp of the last successful refresh; null = never synced. */
  lastSyncedAt: string | null
  /** Last refresh error message (empty string if none). */
  lastError: string
}

/**
 * SeriesLink — one external reference for a series (AniList, MangaDex, the
 * official publisher page, …). Rendered by `LinksRow`/`LinkChip`; the `icon` is
 * an optional explicit lucide name, otherwise the chip derives one from `label`.
 */
export interface SeriesLink {
  /** Human label shown on the pill (e.g. `AniList`, `Official`). */
  label: string
  /** Destination URL — opened in a new tab, always gated through `safeUrl`. */
  url: string
  /** Optional explicit lucide icon name (`lucide:book-open`); auto-derived when absent. */
  icon?: string
}

/**
 * RichSeriesMeta — the richer, Komga-style catalogue metadata a series can carry
 * for the rich card (synopsis, credits, genres/tags, external links), merged by
 * the native metadata engine (`spec/metadata-engine-phase1`). Every field is
 * OPTIONAL: a series never identified against a metadata provider has none of
 * it, and the rich card degrades gracefully when a field is missing/empty.
 * 🔴 `description` is NOT yet mapped from the live API — `SeriesDetailDTO`
 * carries no `description` field today (a Slice-C DTO gap; `Series.description`
 * exists on the backend ent schema but was never threaded through the DTO/
 * OpenAPI spec) — see `useSeriesDetail.mapDetail`'s doc comment.
 */
export interface RichSeriesMeta {
  /** Long-form synopsis; the rich card clamps it behind a "Read more" toggle. */
  description?: string
  /** Alternate / romanised / native titles shown under the main title. */
  altTitles?: string[]
  /** Publication status word (`Ongoing`, `Completed`, `Hiatus`, `Cancelled`). */
  status?: string
  /** First publication year. */
  year?: number
  /** Genre labels (the primary chip row). */
  genres?: string[]
  /** Free-form content tags (the secondary chip row). */
  tags?: string[]
  /** Writer / author credits (art credit is per-source and not modelled here). */
  authors?: string[]
  /** External reference links (tracker + official pages) — the rich card's links row. */
  links?: SeriesLink[]
}

/**
 * SourceRef — provenance descriptor for a merged-metadata or cover pick (the
 * native metadata engine's `SourceRef`): `kind` is `"metadata"` (v1) |
 * `"source"` | `"tracker"` (later); `ref` is the metadata provider Key()
 * (`"anilist"`) or a SeriesProvider UUID, depending on `kind`.
 */
export interface SourceRef {
  kind: string
  ref: string
  remoteId: string
  remoteUrl: string
}

/**
 * SeriesDetail — the full single-series read model: every `SeriesSummary` field
 * plus the chapter and provider feeds. `metadataProviderId` is the source pinned
 * to supply the displayed title + cover (null = auto = highest importance); it
 * backs the (planned) metadata-source picker's active state — a DIFFERENT,
 * older (M10) concept from `metadataSource`/`coverSource` below (which are the
 * native metadata engine's own provenance for the rich fields / chosen cover).
 *
 * It also carries the OPTIONAL `RichSeriesMeta` catalogue fields consumed by the
 * rich card. They are additive + all optional, so every existing consumer and
 * fixture is unaffected.
 */
export interface SeriesDetail extends SeriesSummary, RichSeriesMeta {
  /** Full chapter list (the screen sorts by number then key). */
  chapters: Chapter[]
  /** All tracked sources (the screen sorts by importance descending). */
  providers: Provider[]
  /** Pinned metadata-source id, or null/absent for auto (highest importance). */
  metadataProviderId?: string | null
  /** Provenance of the merged rich metadata; null/absent until the series is identified. */
  metadataSource?: SourceRef | null
  /** Provenance of the chosen cover; null/absent until the owner explicitly picks one. */
  coverSource?: SourceRef | null
}

/** The two mutually-exclusive choices in the required-choice delete dialog. */
export type DeleteChoice = 'keep' | 'wipe'

/**
 * MetadataCandidate — one search result in the "Identify" match flow (Komf-style):
 * a series entry from a metadata provider (AniList / MangaDex / MangaUpdates /
 * MAL / Kitsu) the owner can pick to pull rich metadata + a cover.
 * Presentation-only — the modal renders these and emits the owner's pick; the
 * parent owns the fetch.
 */
export interface MetadataCandidate {
  /** Stable id for single-select (provider-scoped, e.g. `anilist:105398`). */
  id: string
  /**
   * Pretty display label for the provider badge (e.g. `"AniList"`) — NOT the
   * raw key; see `providerKey` for that. The composable maps the backend's
   * provider Key() to this label, falling back to the raw key for a provider
   * the fleet hasn't labelled yet (the set MAY grow — spec §9).
   */
  provider: string
  /** Raw provider Key() (e.g. `"anilist"`) — the `identify()` payload's `provider`. Never rendered directly (use `provider` for display). */
  providerKey: string
  /** The provider's own identifier for this result — the `identify()` payload's `remoteId`. */
  remoteId: string
  /** Series title as this provider knows it. */
  title: string
  /** Portrait cover URL for the result (empty → the initial placeholder). */
  coverUrl: string
  /** First publication year, when the provider reports one. */
  year?: number
}

/**
 * CoverCandidate — one selectable cover in the "Choose cover" picker. In Tsundoku
 * the COVER is chosen INDEPENDENTLY of the metadata match: the owner may take the
 * poster from ANY provider — a tracker (AniList / MAL), a metadata provider
 * (MangaDex / MangaUpdates), or a scraped source ("Asura Scans") — so `provider`
 * is a free display name, not the closed `MetadataProviderName` set. This is a
 * per-field `cover_source` choice, distinct from the Identify flow's whole-series
 * match. Presentation-only: the modal renders these and emits the owner's pick;
 * the parent owns the fetch.
 */
export interface CoverCandidate {
  /** Stable id for single-select (provider-scoped, e.g. `mangadex:cover-2`). */
  id: string
  /** The tracker / metadata-provider / source this cover came from (drives the label). */
  provider: string
  /** Portrait cover URL for the candidate (empty → the initial placeholder). */
  coverUrl: string
  /** "metadata" or "source" — the `setCover()` payload's `sourceKind`. */
  sourceKind: string
  /** Metadata provider Key() (sourceKind "metadata") or SeriesProvider UUID (sourceKind "source") — the `setCover()` payload's `sourceRef`. */
  sourceRef: string
}

/**
 * FractionalCleanupChapter — one already-downloaded FRACTIONAL chapter the owner
 * may remove (`GET /api/series/:id/fractional-cleanup`): a file left behind after
 * "ignore fractional chapters" was ticked on the source(s) carrying it (that
 * toggle stops NEW fractional downloads and deletes nothing).
 *
 * 🔴 `pageCount` IS THE EVIDENCE — `number` and `provider` are only labels. On the
 * owner's real library a "181.5" was a 1-page notice, while "221.5"/"223.5" were
 * 132/135-page FULL chapters that merely carry a ".5" number. Judge by the
 * measurement, never by the name; that is why this is a dialog and not a button.
 */
export interface FractionalCleanupChapter {
  /** Chapter UUID — the id the cleanup POST names. */
  chapterId: string
  /** The chapter number; always fractional (3.1, 181.5 …). */
  number: number
  /** Pages in the downloaded file; null when never recorded (no evidence to judge from). */
  pageCount: number | null
  /** Display label of the source the file came from; "" when that source is gone. */
  provider: string
  /** The CBZ filename that will be deleted. */
  filename: string
}

/**
 * FractionalCleanupPreview — the removable set plus the yardstick that makes it
 * legible. Empty `chapters` = nothing to clean (the panel hides the button).
 */
export interface FractionalCleanupPreview {
  /**
   * MEDIAN page count of the series' WHOLE (non-fractional) downloaded chapters —
   * without it "132p" means nothing. 0 when no whole chapter carries a page count.
   */
  typicalPageCount: number
  /** The removable fractional chapters. */
  chapters: FractionalCleanupChapter[]
}
