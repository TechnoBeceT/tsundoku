/**
 * errorCategory.ts — the display metadata for the backend error taxonomy. The
 * `pkg/errorclass` package (Go) classifies every failed source operation into one
 * of a small, stable set of category strings (captcha, rate_limit, not_found, …);
 * the report renders a coloured `CategoryBadge` for each. Keeping the label + tone
 * for every category in ONE map means the badge, the diagnosis engine, and any
 * future consumer share a single source of truth (§2 DRY) instead of re-deriving
 * the wording/colour inline.
 *
 * 🔴 The KEYS must stay byte-identical to `errorclass.Category*` — they are the
 * stored DB values; a rename here silently mislabels every event.
 */

/** The closed set of category keys emitted by `pkg/errorclass`. */
export type ErrorCategoryKey =
  | 'captcha'
  | 'rate_limit'
  | 'not_found'
  | 'server_error'
  | 'network'
  | 'timeout'
  | 'parse'
  | 'no_pages'
  | 'unknown'

/**
 * CategoryTone — which token treatment a category badge wears. `danger` = the
 * source is actively blocking/failing (rose); `warn` = a recoverable throttle or
 * slowness (amber); `neutral` = benign/informational (grey).
 */
export type CategoryTone = 'danger' | 'warn' | 'neutral'

/** Per-category display: a short human label + the badge tone. */
interface CategoryMeta {
  /** The human-facing badge label. */
  label: string
  /** The badge colour treatment. */
  tone: CategoryTone
}

/** The category → {label, tone} map. Unknown/unmapped keys fall back below. */
export const ERROR_CATEGORIES: Record<ErrorCategoryKey, CategoryMeta> = {
  captcha: { label: 'Anti-bot', tone: 'danger' },
  rate_limit: { label: 'Rate limited', tone: 'warn' },
  not_found: { label: 'Not found', tone: 'neutral' },
  server_error: { label: 'Server error', tone: 'danger' },
  network: { label: 'Network', tone: 'danger' },
  timeout: { label: 'Timeout', tone: 'warn' },
  parse: { label: 'Parse error', tone: 'danger' },
  no_pages: { label: 'No pages', tone: 'neutral' },
  unknown: { label: 'Unknown', tone: 'neutral' },
}

/**
 * categoryMeta — resolve a raw category string (possibly null, possibly an
 * unmapped value) to its display metadata. An absent or unmapped category falls
 * back to the `unknown` treatment so a badge always renders something sensible.
 */
export function categoryMeta(category: string | null | undefined): CategoryMeta {
  if (category != null && category in ERROR_CATEGORIES) {
    return ERROR_CATEGORIES[category as ErrorCategoryKey]
  }
  return ERROR_CATEGORIES.unknown
}
