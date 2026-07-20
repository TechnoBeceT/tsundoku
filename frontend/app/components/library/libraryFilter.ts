import type { SeriesSummary } from '../screens/types'

/**
 * The library grid's boolean toggle-filters, applied on top of the category tab
 * + search. Mirrors the backend `LibraryFilters` DTO so the persisted view state
 * round-trips 1:1. All default false ⇒ the whole library shows.
 */
export interface LibraryFilters {
  /** Only series with ≥1 downloaded chapter. */
  downloaded: boolean
  /** Only series with ≥1 unread downloaded chapter. */
  unread: boolean
  /** Only series the owner marked finished. */
  completed: boolean
  /** Only series with ≥1 dangling (disk-origin, unlinked) provider — even when
   * another source is already matched (the partially-consolidated case). */
  needsSource: boolean
  /** Only series flagged stalled (QCAT-297): no new chapter from any source
   * within the stalled threshold, while still monitored + not completed. */
  stalled: boolean
}

/** The all-off filter state — the default view (whole library). */
export const NO_FILTERS: LibraryFilters = {
  downloaded: false,
  unread: false,
  completed: false,
  needsSource: false,
  stalled: false,
}

/** Case-insensitive, trimmed title search. A blank query matches everything. */
export function searchSeries(items: SeriesSummary[], query: string): SeriesSummary[] {
  const q = query.trim().toLowerCase()
  if (!q) return items
  return items.filter((s) => s.title.toLowerCase().includes(q))
}

/** Filter to one category by NAME; null returns the whole list unchanged. */
export function filterByCategory(items: SeriesSummary[], category: string | null): SeriesSummary[] {
  return category === null ? items : items.filter((s) => s.category === category)
}

/**
 * Apply the boolean toggle-filters. Each active toggle NARROWS (logical AND) —
 * every inactive one is a pass-through. Reads only the present DTO fields
 * (chapterCounts / completed / needsSource), so no new backend data is required.
 * The `needsSource` branch is cover-independent by construction — it reads only
 * `needsSource`, never `coverUrl` (handover 2026-07-13#15: a cover must never be
 * used as a stand-in for "has a source").
 */
export function applyFilters(items: SeriesSummary[], f: LibraryFilters): SeriesSummary[] {
  let out = items
  if (f.downloaded) out = out.filter((s) => s.chapterCounts.downloaded > 0)
  if (f.unread) out = out.filter((s) => s.chapterCounts.unread > 0)
  if (f.completed) out = out.filter((s) => s.completed)
  if (f.needsSource) out = out.filter((s) => s.needsSource)
  if (f.stalled) out = out.filter((s) => s.isStalled)
  return out
}

/** True when any toggle-filter is active (drives the "no matches" empty state). */
export function anyFilterActive(f: LibraryFilters): boolean {
  return f.downloaded || f.unread || f.completed || f.needsSource || f.stalled
}

/**
 * How many series match the query OUTSIDE the active category — the escape
 * hatch's number ("3 matches in other categories").
 *
 * Computed against the WHOLE library on purpose: against the active category it
 * could only ever return 0 and the feature would be silently dead.
 */
export function countMatchesElsewhere(
  all: SeriesSummary[], category: string | null, query: string,
): number {
  if (!query.trim() || category === null) return 0
  return searchSeries(all, query).filter((s) => s.category !== category).length
}
