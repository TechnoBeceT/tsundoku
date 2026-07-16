/**
 * True only for absolute http(s) URLs. Use to gate any window.open / anchor href
 * whose value comes from untrusted upstream data (e.g. a Suwayomi source's manga URL),
 * so a `javascript:`/`data:` URI can never be opened. Mirrors the backend pkg/urlx.IsAbsoluteHTTP rule.
 */
export function isHttpUrl(raw: string | null | undefined): boolean {
  if (!raw) return false
  let parsed: URL
  try {
    parsed = new URL(raw)
  } catch {
    return false
  }
  return parsed.protocol === 'http:' || parsed.protocol === 'https:'
}

/**
 * Returns `raw` only when it is a safe absolute http(s) URL, else `undefined` —
 * the exact shape an `<a v-if="href" :href="href">` wants. One home for the
 * "clickable-or-nothing external href" rule shared by every source-link surface
 * (DiscoverCard's "View on source", the Adopt Configure rows), so a
 * non-http(s)/empty value is never turned into a live link and never falls back
 * to a source-relative addressing url. LinkChip renders its own inert pill, so
 * it keeps using isHttpUrl directly.
 */
export function safeHttpUrl(raw: string | null | undefined): string | undefined {
  return raw && isHttpUrl(raw) ? raw : undefined
}
