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
