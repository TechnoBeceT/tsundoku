/**
 * readableChapters — the readable-chapter gate is the ONE definition shared by the
 * reader (`useReader`), the Series-Detail "Read" button (`ChapterRow`) and the
 * series page's resume FAB. These pin that readability follows the FILE, not the
 * state, which is the rule the backend itself enforces (`series.ChapterPage`
 * refuses only on `filename == ""`).
 *
 * The re-download edge (QCAT-343) is why this matters: it parks a chapter at
 * `wanted` while deliberately KEEPING its CBZ, so it is the first state outside
 * the old downloaded/upgrade_available/upgrading trio to carry a filename. A
 * state-set gate hid a perfectly readable file behind a pending or failed
 * re-download.
 */
import { describe, it, expect } from 'vitest'
import { isReadableChapter, type ReadableChapter } from './readableChapters'

/**
 * A chapter in `state` carrying `filename`. The state rides along only to NAME
 * each case — the predicate deliberately ignores it, which is the whole point —
 * and passing the wider object also proves any richer chapter shape (the
 * generated DTO, the Series-Detail screen type) satisfies `ReadableChapter`
 * structurally.
 */
const chapter = (state: string, filename: string): ReadableChapter & { state: string } => ({ state, filename })

/** The settled states plus every state a re-download passes through. */
const withFile = [
  'downloaded',
  'upgrade_available',
  'upgrading',
  'wanted',
  'downloading',
  'failed',
  'permanently_failed',
]

/** The same states reached the ordinary way — no file was ever rendered. */
const withoutFile = ['wanted', 'downloading', 'failed', 'permanently_failed', 'superseded', 'ignored']

describe('readableChapters', () => {
  it.each(withFile)('a %s chapter WITH a filename is readable', (state) => {
    expect(isReadableChapter(chapter(state, 'x.cbz'))).toBe(true)
  })

  it.each(withoutFile)('a %s chapter with no filename is NOT readable', (state) => {
    expect(isReadableChapter(chapter(state, ''))).toBe(false)
  })

  it('is readable while a re-download is pending — the CBZ is deliberately kept', () => {
    expect(isReadableChapter(chapter('wanted', '[Comix][en] Saga 0001.cbz'))).toBe(true)
  })

  it('is still readable after a re-download failed for good', () => {
    expect(isReadableChapter(chapter('permanently_failed', '[Comix][en] Saga 0001.cbz'))).toBe(true)
  })

  it('is NOT readable for a chapter that was never downloaded', () => {
    expect(isReadableChapter(chapter('wanted', ''))).toBe(false)
  })

  it('is NOT readable once split-part suppression cleared the filename', () => {
    expect(isReadableChapter(chapter('superseded', ''))).toBe(false)
  })
})
