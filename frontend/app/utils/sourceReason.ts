/**
 * humanizeSourceReason — maps a backend errorclass category (the `reason` field of
 * a cooling source, e.g. "rate_limit" / "server_error") to a short human label for
 * the source-status strip's cooling badge ("cooling 12m (rate-limited)").
 *
 * The categories mirror internal/pkg/errorclass. An unknown/unmapped category
 * falls back to its underscores-to-spaces form, so a new backend category still
 * renders legibly rather than raw.
 */
const REASON_LABELS: Record<string, string> = {
  rate_limit: 'rate-limited',
  captcha: 'blocked (captcha)',
  server_error: 'server error',
  timeout: 'timeout',
  network: 'network error',
  not_found: 'not found',
  parse: 'parse error',
  no_pages: 'no pages',
  unknown: 'error',
}

export function humanizeSourceReason(category: string): string {
  return REASON_LABELS[category] ?? category.replace(/_/g, ' ')
}
