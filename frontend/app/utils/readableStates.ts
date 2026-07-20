/**
 * readableStates — the ONE definition of which chapter states the in-app reader
 * can open and page through (shared by `useReader` — the reader's chapter list —
 * and `ChapterRow` — the Series-Detail "Read" button gate — so neither surface
 * re-declares the set, §2 DRY).
 *
 * A chapter is readable whenever a valid CBZ is on disk, which is true for all
 * three of these states, NOT just `downloaded`:
 *   - `downloaded`        — the settled state.
 *   - `upgrade_available` — a better source was detected; the OLD source's CBZ
 *     is still intact (the convergence engine deletes it only AFTER the new one
 *     succeeds — `tryDeleteOldCBZ` runs on convergence success only).
 *   - `upgrading`         — the upgrade fetch is in flight; the old CBZ likewise
 *     stays on disk until the replacement lands.
 * Gating the reader (or the "Read" button) to `downloaded` alone made a chapter
 * pending/undergoing an upgrade (and a whole series parked in `upgrade_available`
 * by a source ban) unreadable for no reason — `filename`/`pageCount`/`pageVersion`
 * are all intact across an upgrade, so the page-bytes endpoint serves them
 * unchanged (the backend gates on `filename == ""`, not on state).
 */

/** The chapter states whose CBZ is on disk and therefore readable. */
export type ReadableState = 'downloaded' | 'upgrade_available' | 'upgrading'

/** The readable chapter states as a lookup set (see `isReadableState`). */
export const READABLE_STATES: ReadonlySet<ReadableState> = new Set<ReadableState>([
  'downloaded',
  'upgrade_available',
  'upgrading',
])

/**
 * isReadableState — whether a chapter in `state` has an on-disk CBZ the reader
 * can open. Accepts any state string (both the generated `Chapter.state` and the
 * hand-written `ChapterState` union flow in) and narrows against `READABLE_STATES`.
 */
export function isReadableState(state: string): boolean {
  return (READABLE_STATES as ReadonlySet<string>).has(state)
}
