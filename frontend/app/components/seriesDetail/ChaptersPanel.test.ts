/**
 * ChaptersPanel — the Series-Detail "Chapters" card.
 *
 * The panel receives its chapters ALREADY sorted latest-first (descending) from
 * the screen. These tests pin the local Komikku-parity direction toggle: it
 * defaults to descending (renders the incoming order untouched) and flipping it
 * reverses the displayed rows in memory WITHOUT re-emitting or refetching.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ChaptersPanel from './ChaptersPanel.vue'
import ChapterRow from './ChapterRow.vue'
import type { Chapter } from '../screens/seriesDetail.types'

function chapter(over: Partial<Chapter> & { chapterKey: string }): Chapter {
  return {
    id: over.id ?? over.chapterKey,
    chapterKey: over.chapterKey,
    number: over.number ?? null,
    name: over.name ?? '',
    state: over.state ?? 'downloaded',
    filename: over.filename ?? '',
    pageCount: over.pageCount ?? null,
    read: over.read ?? false,
    lastReadPage: over.lastReadPage ?? 0,
    readAt: over.readAt ?? null,
    releaseDate: over.releaseDate ?? null,
  }
}

// Incoming order = descending (latest-first), as the screen sorts it.
const chapters: Chapter[] = [
  chapter({ chapterKey: 'c3', number: 3 }),
  chapter({ chapterKey: 'c2', number: 2 }),
  chapter({ chapterKey: 'c1', number: 1 }),
]

function renderedKeys(w: ReturnType<typeof mount>): string[] {
  return w.findAllComponents(ChapterRow).map((r) => (r.props('chapter') as Chapter).chapterKey)
}

describe('ChaptersPanel — direction toggle', () => {
  it('defaults to descending — renders the incoming (latest-first) order', () => {
    const w = mount(ChaptersPanel, { props: { chapters, total: 3 } })

    expect(renderedKeys(w)).toEqual(['c3', 'c2', 'c1'])
    expect(w.get('button[aria-label*="Chapter order"]').attributes('aria-label')).toContain('Descending')
  })

  it('flipping the toggle reverses the displayed order to ascending', async () => {
    const w = mount(ChaptersPanel, { props: { chapters, total: 3 } })

    await w.get('button[aria-label*="Chapter order"]').trigger('click')

    expect(renderedKeys(w)).toEqual(['c1', 'c2', 'c3'])
    expect(w.get('button[aria-label*="Chapter order"]').attributes('aria-label')).toContain('Ascending')
  })

  it('flipping it back restores descending — a pure presentation flip, no emits', async () => {
    const w = mount(ChaptersPanel, { props: { chapters, total: 3 } })
    const toggle = w.get('button[aria-label*="Chapter order"]')

    await toggle.trigger('click')
    await toggle.trigger('click')

    expect(renderedKeys(w)).toEqual(['c3', 'c2', 'c1'])
    expect(w.emitted('read')).toBeUndefined()
    expect(w.emitted('set-current')).toBeUndefined()
  })
})

/**
 * Re-download relay (QCAT-343). The panel owns no mutation: it forwards the
 * row's request up and marks exactly the row named by `redownloadingId` as busy,
 * so one chapter's in-flight fetch never freezes the rest of the table.
 */
describe('ChaptersPanel — re-download relay', () => {
  it('forwards a row\'s `redownload` up with the chapter id', async () => {
    const w = mount(ChaptersPanel, { props: { chapters, total: 3 } })
    w.findAllComponents(ChapterRow)[1]!.vm.$emit('redownload', 'c2')
    await w.vm.$nextTick()

    expect(w.emitted('redownload')?.[0]).toEqual(['c2'])
  })

  it('marks ONLY the in-flight chapter as re-downloading', () => {
    const w = mount(ChaptersPanel, { props: { chapters, total: 3, redownloadingId: 'c2' } })
    const busy = w.findAllComponents(ChapterRow).map((r) => r.props('redownloading'))

    expect(busy).toEqual([false, true, false])
  })

  it('marks no chapter when nothing is in flight', () => {
    const w = mount(ChaptersPanel, { props: { chapters, total: 3, redownloadingId: null } })

    expect(w.findAllComponents(ChapterRow).every((r) => r.props('redownloading') === false)).toBe(true)
  })
})
