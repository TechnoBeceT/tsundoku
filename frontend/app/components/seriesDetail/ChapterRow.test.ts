/**
 * ChapterRow — in-app reader progress rendering (Task 7).
 *
 * Pins the three mutually-exclusive read states the row promises: read
 * (dimmed, no dot), unread (full-strength, dot), and partially-read (a resume
 * line). `lastReadPage` is 0-based; the resume line displays 1-based
 * ("Page 18 / 165" for `lastReadPage: 17`) — the off-by-one this guards.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ChapterRow from './ChapterRow.vue'
import type { Chapter } from '../screens/seriesDetail.types'

const base: Chapter = {
  id: 'chapter-1',
  chapterKey: 'ch-1',
  number: 1,
  name: 'The Weakest Hunter',
  state: 'downloaded',
  filename: '[mangadex][en] Solo Leveling 0001.cbz',
  pageCount: 165,
  read: false,
  lastReadPage: 0,
  readAt: null,
  releaseDate: null,
}

function render(over: Partial<Chapter> = {}, extra: { redownloading?: boolean } = {}) {
  return mount(ChapterRow, { props: { chapter: { ...base, ...over }, ...extra } })
}

/** The re-download action, found by its accessible name. */
const redownloadButton = (w: ReturnType<typeof render>) =>
  w.findAll('button').find((b) => b.attributes('aria-label')?.startsWith('Re-download chapter'))

describe('ChapterRow — read state', () => {
  it('dims a read chapter and shows no unread dot', () => {
    const w = render({ read: true, lastReadPage: 164, readAt: '2026-07-01T00:00:00Z' })

    expect(w.classes()).toContain('chapter--read')
    expect(w.find('.chapter__dot').exists()).toBe(false)
  })

  it('shows an unread dot on an unread chapter, full strength', () => {
    const w = render({ read: false, lastReadPage: 0 })

    expect(w.classes()).not.toContain('chapter--read')
    expect(w.find('.chapter__dot').exists()).toBe(true)
    expect(w.find('.chapter__resume').exists()).toBe(false)
  })

  it('shows the resume line on a partially-read chapter, 1-based ("Page 18 / 165" for lastReadPage: 17)', () => {
    const w = render({ read: false, lastReadPage: 17, pageCount: 165 })

    expect(w.find('.chapter__resume').text()).toBe('Page 18 / 165')
    // Partially read is distinct from unread — no dot once there's progress.
    expect(w.find('.chapter__dot').exists()).toBe(false)
  })

  it('shows no resume line when lastReadPage is 0 (that is the unread case, not partially-read)', () => {
    const w = render({ read: false, lastReadPage: 0 })

    expect(w.find('.chapter__resume').exists()).toBe(false)
  })
})

/**
 * The "Read" button gates on `isReadableChapter` — the presence of a CBZ, NOT the
 * state. `upgrade_available`/`upgrading` keep their old CBZ while a better source
 * is pending, and a re-download (QCAT-343) parks the chapter back at `wanted`
 * while deliberately KEEPING the file, so all of them stay readable; a chapter
 * with no filename never was.
 */
describe('ChapterRow — read button visibility', () => {
  const readButton = (over: Partial<Chapter>) =>
    render(over).findAll('button').find((b) => b.text() === 'Read')

  it('renders the read button for a downloaded chapter', () => {
    expect(readButton({ state: 'downloaded' })?.exists()).toBe(true)
  })

  it('renders the read button for an upgrade_available chapter (old CBZ still on disk)', () => {
    expect(readButton({ state: 'upgrade_available' })?.exists()).toBe(true)
  })

  it('renders the read button for an upgrading chapter (old CBZ still on disk)', () => {
    expect(readButton({ state: 'upgrading' })?.exists()).toBe(true)
  })

  it('keeps the read button while a re-download is pending (wanted, but the CBZ was kept)', () => {
    expect(readButton({ state: 'wanted', filename: 'kept.cbz' })?.exists()).toBe(true)
  })

  it('keeps the read button when a re-download failed for good (the old CBZ is still there)', () => {
    expect(readButton({ state: 'permanently_failed', filename: 'kept.cbz' })?.exists()).toBe(true)
  })

  it('emits `read` with the chapter id when the button is clicked', async () => {
    const w = render({ state: 'upgrade_available' })
    await w.findAll('button').find((b) => b.text() === 'Read')!.trigger('click')

    expect(w.emitted('read')?.[0]).toEqual(['chapter-1'])
  })

  it('hides the read button for a chapter that was never downloaded (wanted, no file)', () => {
    expect(readButton({ state: 'wanted', filename: '' })).toBeUndefined()
  })

  it('hides the read button for a failed chapter with no file', () => {
    expect(readButton({ state: 'failed', filename: '' })).toBeUndefined()
  })

  it('hides the read button once split-part suppression cleared the filename', () => {
    expect(readButton({ state: 'superseded', filename: '' })).toBeUndefined()
  })
})

describe('ChapterRow — release date (QCAT-297)', () => {
  it('renders a relative release date when releaseDate is set', () => {
    const threeDaysAgo = new Date(Date.now() - 3 * 86_400_000).toISOString()
    const w = render({ releaseDate: threeDaysAgo })

    expect(w.find('.chapter__released').exists()).toBe(true)
    expect(w.find('.chapter__released').text()).toBe('3d ago')
  })

  it('shows no release marker when releaseDate is null (never dated, never downloaded)', () => {
    const w = render({ releaseDate: null })

    expect(w.find('.chapter__released').exists()).toBe(false)
  })
})

/**
 * Re-download (QCAT-343) — the row's newest control, to the RIGHT of the state
 * badge.
 *
 * It is offered ONLY for `downloaded`: the API answers 409 for anything else,
 * because a re-download replaces a file that exists. Deliberately NARROWER than
 * the "Read" button, which also covers `upgrade_available`/`upgrading` — those
 * keep an old CBZ on disk but are mid-convergence, and the engine owns them.
 *
 * The row only EMITS; the page runs the mutation. There is no confirm gate and
 * none is wanted — the re-download deletes nothing.
 */
describe('ChapterRow — re-download', () => {
  it('offers the action on a downloaded chapter, to the right of the state badge', () => {
    const w = render({ state: 'downloaded' })
    const btn = redownloadButton(w)

    expect(btn).toBeTruthy()
    // Order within the controls cluster is part of the contract: badge, then action.
    const controls = w.find('.chapter__controls').element
    const nodes = Array.from(controls.children)
    const badgeIndex = nodes.findIndex((n) => n.classList.contains('badge'))
    const buttonIndex = nodes.findIndex((n) => n === btn!.element || n.contains(btn!.element))
    // Both must actually be found — a -1 on either side would make the
    // comparison below pass for the wrong reason.
    expect(badgeIndex).toBeGreaterThanOrEqual(0)
    expect(buttonIndex).toBeGreaterThan(badgeIndex)
  })

  it('emits `redownload` with the chapter id', async () => {
    const w = render({ state: 'downloaded' })
    await redownloadButton(w)!.trigger('click')

    expect(w.emitted('redownload')?.[0]).toEqual(['chapter-1'])
  })

  it('disables the action while that chapter\'s re-download is in flight (§16)', () => {
    const w = render({ state: 'downloaded' }, { redownloading: true })

    expect(redownloadButton(w)!.attributes('disabled')).toBeDefined()
  })

  it.each(['wanted', 'failed', 'permanently_failed', 'upgrade_available', 'upgrading'] as const)(
    'hides the action for %s — only a downloaded chapter has a stored file to replace',
    (state) => {
      expect(redownloadButton(render({ state }))).toBeUndefined()
    },
  )
})

// GAP-141: a chapter a source is WITHHOLDING behind coins rests in `failed` because
// the fetch produced no file — but nothing is broken and no owner action helps, so
// the red "Failed" pill is replaced by a calm early-access badge naming the return
// date. Every other chapter keeps its state badge unchanged.
describe('ChapterRow — early access', () => {
  it('replaces the state badge with an early-access badge on a withheld chapter', () => {
    const w = render({
      state: 'failed',
      locked: true,
      lockedUntil: new Date(Date.now() + 2 * 24 * 3_600_000).toISOString(),
    })

    expect(w.find('.early').text()).toContain('Early access')
    expect(w.find('.early').text()).toContain('free ~2d')
    expect(w.find('.badge').exists()).toBe(false)
  })

  it('keeps the state badge on an ordinary failure', () => {
    const w = render({ state: 'failed' })

    expect(w.find('.early').exists()).toBe(false)
    expect(w.find('.badge').text()).toContain('Failed')
  })
})
