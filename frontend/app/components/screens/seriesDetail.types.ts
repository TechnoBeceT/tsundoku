/**
 * Prop/data types for the Series Detail screen (`GET /api/series/:id`).
 *
 * `SeriesDetail` is everything `SeriesSummary` carries (re-used from the shared
 * `screens/types.ts`) PLUS the full chapter and provider feeds. Like the other
 * screen types these are hand-light + presentation-only — the screen receives
 * everything via props and never imports the generated API client.
 */
import type { SeriesSummary } from './types'

/** The seven download states a chapter moves through (the backend state machine). */
export type ChapterState =
  | 'wanted'
  | 'downloading'
  | 'downloaded'
  | 'upgrade_available'
  | 'upgrading'
  | 'failed'
  | 'permanently_failed'

/** Per-provider health: current, gone stale, or erroring on last refresh. */
export type ProviderHealth = 'ok' | 'stale' | 'erroring'

/**
 * Chapter — one row in the chapter table. Identity is `chapterKey` (never the
 * `number`, which may be null and is display/sort only). `filename` is non-empty
 * only once the CBZ is on disk; `pageCount` is null until then.
 */
export interface Chapter {
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
 * SeriesDetail — the full single-series read model: every `SeriesSummary` field
 * plus the chapter and provider feeds. `metadataProviderId` is the source pinned
 * to supply the displayed title + cover (null = auto = highest importance); it
 * backs the (planned) metadata-source picker's active state.
 */
export interface SeriesDetail extends SeriesSummary {
  /** Full chapter list (the screen sorts by number then key). */
  chapters: Chapter[]
  /** All tracked sources (the screen sorts by importance descending). */
  providers: Provider[]
  /** Pinned metadata-source id, or null/absent for auto (highest importance). */
  metadataProviderId?: string | null
}

/** The two mutually-exclusive choices in the required-choice delete dialog. */
export type DeleteChoice = 'keep' | 'wipe'
