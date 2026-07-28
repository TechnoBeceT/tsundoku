/**
 * fileSize.ts — human-readable byte counts.
 *
 * Extracted as a util (not inlined in a card) because the Cleanup console shows
 * the same reclaimable figure in two places — the per-series card and the page
 * header total — and a second copy is how two "same" numbers start rendering
 * differently.
 */

/** The unit ladder, binary base. Anything past TB is expressed in TB. */
const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'] as const

/**
 * formatBytes — render a byte count as "512 B" / "1.0 KB" / "2.9 GB".
 *
 * Base 1024 (what a filesystem reports), one decimal from KB up, whole numbers
 * for plain bytes. A negative or non-finite input renders "0 B": those cannot
 * describe a real file, and showing "NaN GB" next to a delete affordance is worse
 * than showing nothing.
 *
 * @param bytes total size in bytes
 * @returns the formatted size, always with a unit suffix
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'

  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit += 1
  }
  return unit === 0 ? `${Math.round(value)} B` : `${value.toFixed(1)} ${UNITS[unit]}`
}
