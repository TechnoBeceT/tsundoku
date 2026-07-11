/**
 * ReaderPageSlider.logic — the PURE math behind the reader's page slider.
 *
 * The slider maps a 0-based page index within the CURRENT chapter onto a 0..1
 * track fraction and back. It is deliberately driven by the chapter's TRIMMED
 * visible page count, never its declared `pageCount`: a declared count (from
 * ComicInfo on Kaizoku imports) may exceed the CBZ's real image count, so a
 * slider trusting it would stall short of the end on exactly those chapters.
 *
 * No DOM, no Vue reactivity here — plain values in, plain values out.
 */

/** Above this many pages, per-page tick dots stop being a scale and become a
 *  smear (a 165-page chapter renders a solid bar), so the track goes plain. */
export const SLIDER_TICK_MAX_PAGES = 30

/** Whether to render per-page tick dots for a chapter of this many pages. */
export function showTicks(visiblePages: number): boolean {
  return visiblePages > 0 && visiblePages <= SLIDER_TICK_MAX_PAGES
}

/**
 * pageToFraction — the 0..1 track position of a 0-based page. The LAST page is 1
 * (not `page/count`), so a finished chapter shows a full track rather than
 * stopping one page short. A 0- or 1-page chapter has no span: always 0.
 */
export function pageToFraction(page: number, visiblePages: number): number {
  if (visiblePages <= 1) return 0
  const clamped = Math.min(Math.max(page, 0), visiblePages - 1)
  return clamped / (visiblePages - 1)
}

/**
 * fractionToPage — the inverse: the 0-based page under a 0..1 track fraction.
 * Clamps out-of-range fractions (a drag can overshoot the track) to the valid
 * page span, and rounds to the nearest page so a drag snaps rather than drifts.
 */
export function fractionToPage(fraction: number, visiblePages: number): number {
  if (visiblePages <= 1) return 0
  const clamped = Math.min(Math.max(fraction, 0), 1)
  return Math.round(clamped * (visiblePages - 1))
}
