/**
 * SeriesCard — the unread-chapter badge on the cover corner.
 *
 * `chapterCounts.unread` is downloaded-but-unread chapters — what the owner
 * can read RIGHT NOW, deliberately not every chapter a source knows about.
 * Pins the two states: a positive count renders the badge (with the exact
 * number), and zero hides it ENTIRELY — the badge's presence is the signal,
 * so a rendered "0" would be worse than no badge at all.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SeriesCard from './SeriesCard.vue'
import type { SeriesSummary } from '../screens/types'

const base: SeriesSummary = {
  id: 'series-1',
  title: 'Solo Leveling',
  slug: 'solo-leveling',
  category: 'Manhwa',
  coverUrl: '',
  monitored: true,
  completed: false,
  chapterCounts: { total: 20, downloaded: 20, wanted: 0, failed: 0, unread: 0 },
  createdAt: '2024-01-01T00:00:00Z',
  lastChapterDownloadedAt: null,
}

function render(unread: number) {
  return mount(SeriesCard, {
    props: {
      series: { ...base, chapterCounts: { ...base.chapterCounts, unread } },
    },
  })
}

describe('SeriesCard — unread badge', () => {
  it('shows the unread count badge', () => {
    const w = render(12)

    expect(w.find('.card__unread').exists()).toBe(true)
    expect(w.find('.card__unread').text()).toBe('12')
  })

  it('hides the badge entirely when there is nothing unread', () => {
    const w = render(0)

    expect(w.find('.card__unread').exists()).toBe(false)
  })
})
