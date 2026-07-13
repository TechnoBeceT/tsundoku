/**
 * LibraryList — the three honest empty states (Komikku search model). Pins the
 * behaviour a single "No series in this category yet." message would get wrong
 * the moment search exists:
 *   1. NO search + empty category → "No series in this category yet."
 *   2. search + nothing matches ANYWHERE → "No series match '<query>'."
 *   3. search + nothing here but N elsewhere → the escape hatch: an "N matches
 *      in other categories" line + a widen button that emits `searchEverywhere`.
 *
 * Non-vacuous: collapse the three-way branch back to one message and the
 * search-active assertions fail; drop the widen button's click wiring and the
 * escape-hatch emit assertion fails.
 *
 * Mounts the REAL component (mirrors ScanLibrary.test.ts).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import LibraryList from './LibraryList.vue'
import type { CategorySummary, SeriesSummary } from './types'

const categories: CategorySummary[] = [
  { category: 'Manga', count: 4 },
  { category: 'Manhwa', count: 3 },
]

function mountList(props: {
  series?: SeriesSummary[]
  search?: string
  matchesElsewhere?: number
  activeCategory?: string | null
}) {
  return mount(LibraryList, {
    props: {
      series: props.series ?? [],
      categories,
      activeCategory: props.activeCategory ?? null,
      search: props.search ?? '',
      sortKey: 'title' as const,
      sortDir: 'asc' as const,
      matchesElsewhere: props.matchesElsewhere ?? 0,
    },
  })
}

describe('LibraryList empty states', () => {
  it('shows the category-empty message when there is no search', () => {
    const wrapper = mountList({ series: [], search: '' })
    expect(wrapper.text()).toContain('No series in this category yet.')
  })

  it('shows the no-match message when nothing matches anywhere', () => {
    const wrapper = mountList({ series: [], search: 'zzz', matchesElsewhere: 0 })
    expect(wrapper.text()).toContain("No series match 'zzz'.")
    expect(wrapper.text()).not.toContain('No series in this category yet.')
  })

  it('offers the escape hatch when matches exist in OTHER categories', async () => {
    const wrapper = mountList({
      series: [],
      search: 'solo',
      matchesElsewhere: 3,
      activeCategory: 'Manhwa',
    })

    expect(wrapper.text()).toContain('3 matches in other categories')

    const widen = wrapper.get('[data-test="widen-search"]')
    await widen.trigger('click')
    expect(wrapper.emitted('searchEverywhere')).toHaveLength(1)
  })
})
