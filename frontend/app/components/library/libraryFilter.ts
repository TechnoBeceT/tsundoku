import type { SeriesSummary } from '../screens/types'

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
