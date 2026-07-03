/**
 * Prop/data types for the Discover (per-source catalog browse) screen.
 *
 * These mirror the backend's browse read-model (`GET /api/sources` +
 * the planned `GET /api/sources/{sourceId}/browse`) but stay hand-light and
 * presentation-only — the screen receives everything via props and never imports
 * the generated API client (kept in this `.ts`, never a `.vue`, so stories and
 * fixtures can import the types freely).
 */

/** Which per-source listing to browse. Switching one refetches page 1. */
export type BrowseType = 'popular' | 'latest'

/**
 * DiscoverSource — one entry in the source picker (from `GET /api/sources`).
 */
export interface DiscoverSource {
  /** Suwayomi source ID (string — a 64-bit int on the wire). */
  id: string
  /** Human-readable source name (e.g. "MangaDex"). */
  name: string
  /** Content language of this source (e.g. "en"). */
  lang: string
}

/**
 * DiscoverCandidate — one manga card in the browse grid. Mirrors the backend
 * `SearchCandidate` (so browse + search cards look identical) plus a few optional
 * preview extras the hover popup shows when the source provides them.
 *
 * A candidate is NOT in the library yet, so a click opens the Adopt/Inspect flow
 * (never a series-detail route — that only exists for adopted series).
 */
export interface DiscoverCandidate {
  /** Suwayomi source ID this candidate came from. */
  source: string
  /** Human-readable source name (shown in the preview popup). */
  sourceName: string
  /** Content language of the source (e.g. "en"). */
  lang: string
  /** Suwayomi-internal manga identifier within the source. */
  mangaId: number
  /** Manga display title as returned by the source. */
  title: string
  /** Cover image URL, or "" → the initial-letter placeholder. */
  thumbnailUrl: string
  /** Provider-canonical URL — the "View on source ↗" external link target. */
  url: string
  /** Subtle "In library" marker when this manga is already adopted. */
  inLibrary?: boolean
  /** Synopsis shown in the hover preview popup, when the source provides one. */
  description?: string
  /** Genre tags shown as chips in the hover preview popup, when available. */
  genres?: string[]
  /** Writing credit shown in the hover preview popup, when the source provides one. */
  author?: string
  /** Art credit shown in the hover preview popup, when the source provides one and
   *  it differs from `author` (a single-credit work only shows `author`). */
  artist?: string
}

/**
 * BrowseResult — one page of a source's Popular/Latest listing. `manga` is the
 * accumulated, possibly-appended list (the parent appends each loaded page);
 * `hasNextPage` drives the "Load more" affordance.
 */
export interface BrowseResult {
  /** Candidates loaded so far, in source order. */
  manga: DiscoverCandidate[]
  /** Whether another page exists after the current one. */
  hasNextPage: boolean
  /** The 1-based page number most recently returned. */
  page: number
}
