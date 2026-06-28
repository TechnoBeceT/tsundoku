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
