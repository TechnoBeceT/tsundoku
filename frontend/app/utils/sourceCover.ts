/**
 * sourceCoverProxyUrl — builds the same-origin cover-proxy URL for a
 * source-manga thumbnail.
 *
 * Discover/Search cards used to set an <img src> straight to the raw source
 * URL a candidate's thumbnailUrl carries. An open-CDN source (e.g. Asura)
 * tolerates the browser fetching that directly, but a Cloudflare/hotlink-
 * protected source (e.g. The Blank) returns 403 to a plain browser request —
 * the cover renders blank. `GET /api/sources/{sourceId}/cover?url=` re-fetches
 * the image through the backend → engine host, whose outbound HTTP client
 * already carries that source's cf_clearance, then streams the bytes back
 * same-origin. Cookie auth rides along with a plain <img src> for free — no
 * header needed (mirrors how the library-series cover proxy already works).
 *
 * Returns "" when there is no thumbnail, so every existing
 * `v-if="candidate.thumbnailUrl"` placeholder check keeps working unchanged.
 */
export function sourceCoverProxyUrl(source: string, thumbnailUrl: string): string {
  if (!thumbnailUrl) return ''
  return `/api/sources/${encodeURIComponent(source)}/cover?url=${encodeURIComponent(thumbnailUrl)}`
}
