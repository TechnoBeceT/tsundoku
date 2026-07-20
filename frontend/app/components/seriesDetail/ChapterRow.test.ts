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

function render(over: Partial<Chapter> = {}) {
  return mount(ChapterRow, { props: { chapter: { ...base, ...over } } })
}

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
 * The "Read" button gates on the reader's READABLE_STATES (a CBZ is on disk),
 * NOT `downloaded` alone: `upgrade_available`/`upgrading` keep their old CBZ on
 * disk while a better source is pending, so the owner can still read them.
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

  it('emits `read` with the chapter id when the button is clicked', async () => {
    const w = render({ state: 'upgrade_available' })
    await w.findAll('button').find((b) => b.text() === 'Read')!.trigger('click')

    expect(w.emitted('read')?.[0]).toEqual(['chapter-1'])
  })

  it('hides the read button for a non-readable state (wanted)', () => {
    expect(readButton({ state: 'wanted' })).toBeUndefined()
  })

  it('hides the read button for a non-readable state (failed)', () => {
    expect(readButton({ state: 'failed' })).toBeUndefined()
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
