/**
 * readerChrome.logic.ts — the pure decision behind the reader's tap-to-toggle
 * chrome. Kept out of the route SFC so it is unit-testable in isolation (mirrors
 * ReaderStrip.logic.ts).
 */

/** The fraction of the viewport height at the top and bottom that counts as an
 *  "edge" zone — a tap there does NOT toggle the chrome (it's near the bars). */
const DEFAULT_EDGE_FRACTION = 0.22

/**
 * isCenterTap — true when a tap at `clientY` falls in the vertical CENTRE band of
 * the viewport (outside the top/bottom `edgeFraction` margins). The reader uses
 * this so a centre tap toggles the chrome while a tap near either edge — where
 * the chrome bars live — is ignored. A non-positive `viewportHeight` (unmeasured)
 * is never a centre tap.
 */
export function isCenterTap(
  clientY: number,
  viewportHeight: number,
  edgeFraction: number = DEFAULT_EDGE_FRACTION,
): boolean {
  if (viewportHeight <= 0) return false
  const edge = viewportHeight * edgeFraction
  return clientY >= edge && clientY <= viewportHeight - edge
}
