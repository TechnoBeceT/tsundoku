/**
 * Story-only fixtures for the Categories overview screen. NOT imported by app
 * code — only by Storybook stories — so the screen stays props-driven and
 * backend-free.
 *
 * A dynamic, arbitrary-length list (NOT the fixed legacy five) including a couple
 * of zero-count categories, so the dashboard exercises both populated and empty
 * cards (a 0% share still renders as a selectable card).
 */
import type { CategorySummary } from '../components/screens/types'

/** A varied distribution: several populated categories plus some empty ones. */
export const categories: CategorySummary[] = [
  { category: 'Manhwa', count: 14 },
  { category: 'Manga', count: 11 },
  { category: 'Manhua', count: 5 },
  { category: 'Webtoons', count: 3 },
  { category: 'Comic', count: 0 },
  { category: 'Other', count: 0 },
]
