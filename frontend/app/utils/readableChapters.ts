/**
 * readableChapters — the ONE definition of which chapters the in-app reader can
 * open and page through (shared by `useReader` — the reader's chapter list —
 * `ChapterRow` — the Series-Detail "Read" button gate — and the series page's
 * resume FAB, so no surface re-declares the rule, §2 DRY).
 *
 * 🔴 Readability follows the FILE, not the state. A chapter is readable exactly
 * when a rendered CBZ is on disk, and `filename` is the record of that — which is
 * the rule the backend already enforces: `series.ChapterPage` refuses only when
 * `filename == ""`, never on the state, so the page bytes are served for any
 * chapter that has one. Nothing but `download.supersedeOnePart` ever clears
 * `filename`, and it deletes the file in the same breath, so the column and the
 * disk agree.
 *
 * This deliberately REPLACED an allow-list of states (`downloaded` /
 * `upgrade_available` / `upgrading`). That list was a proxy for "a CBZ is on
 * disk", and it stopped being an accurate one when the owner re-download landed
 * (QCAT-343): a re-download parks a chapter back at `wanted` while KEEPING its
 * CBZ, precisely so a failed re-fetch leaves a readable file rather than nothing.
 * Under the old gate, triggering one made the "Read" button vanish and dropped the
 * chapter out of the reader, and a re-download that failed for good hid a
 * perfectly readable file for as long as it stayed failed. Testing the file
 * instead gets every future state that keeps one right for free.
 *
 * A chapter that was never downloaded still carries `filename === ''`, so it is
 * still not readable — that is the other half of the contract.
 */

/**
 * ReadableChapter — the minimum a chapter must expose to be tested for
 * readability. Structural, so both the generated `Chapter` DTO and the
 * hand-written Series-Detail `Chapter` satisfy it without a cast.
 */
export interface ReadableChapter {
  /** Rendered CBZ filename; empty exactly when no file is on disk. */
  filename: string
}

/**
 * isReadableChapter — whether `chapter` has an on-disk CBZ the reader can open.
 * The download state is deliberately not consulted; see the file header for why.
 */
export function isReadableChapter(chapter: ReadableChapter): boolean {
  return chapter.filename !== ''
}
