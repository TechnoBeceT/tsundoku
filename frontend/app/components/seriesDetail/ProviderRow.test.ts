/**
 * ProviderRow — the source-coverage line.
 *
 * The row must state, with NO click and NO fetch, both of the numbers that used
 * to be conflated into a misleading bare "N chapters":
 *   1. what the source OFFERS — `feedCount` + `feedRanges` from the stored feed
 *      ("270 chapters · 1-269");
 *   2. what it currently SUPPLIES — `chapterCount` ("supplies 8").
 * A provider with no stored feed says "No chapter feed", never "0 chapters".
 *
 * The regression this guards: the old row hid the offering behind a "Show
 * coverage" button that fired a LIVE per-source fetch for a number we already
 * store. There is no such affordance any more — the assertions below pin that.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ProviderRow from './ProviderRow.vue'
import type { Provider } from '../screens/seriesDetail.types'

const base: Provider = {
  id: 'prov-1',
  provider: 'src-1',
  providerName: 'MangaDex',
  linked: true,
  mangaId: 42,
  chapterCount: 8,
  feedCount: 270,
  feedRanges: '1-269',
  hasFeed: true,
  scanlator: '',
  language: 'en',
  importance: 30,
  health: 'ok',
  chaptersBehind: 0,
  newestChapterAt: null,
  lastSyncedAt: null,
  lastError: '',
}

function render(over: Partial<Provider> = {}) {
  return mount(ProviderRow, {
    props: {
      provider: { ...base, ...over },
      rank: 1,
      preferred: true,
      canUp: false,
      canDown: true,
    },
  })
}

describe('ProviderRow — coverage line', () => {
  it('shows the source\'s offering (count + ranges) and the supplied count, with no click', () => {
    const w = render()

    expect(w.find('.source__offering').text()).toBe('270 chapters · 1-269')
    expect(w.find('.source__supplies').text()).toBe('supplies 8')
  })

  it('omits the ranges when the feed carries no chapter numbers', () => {
    const w = render({ feedRanges: '' })

    expect(w.find('.source__offering').text()).toBe('270 chapters')
  })

  it('singularises a one-chapter feed', () => {
    const w = render({ feedCount: 1, feedRanges: '1' })

    expect(w.find('.source__offering').text()).toBe('1 chapter · 1')
  })

  it('says "No chapter feed" for an empty feed — never "0 chapters"', () => {
    const w = render({ feedCount: 0, feedRanges: '', hasFeed: false, chapterCount: 45 })

    expect(w.find('.source__offering').text()).toBe('No chapter feed')
    expect(w.find('.source__offering').text()).not.toContain('0 chapters')
    expect(w.find('.source__supplies').text()).toBe('supplies 45')
  })

  it('renders no "Show coverage" affordance — the coverage is never fetched from the source', () => {
    const w = render()

    expect(w.text()).not.toContain('Show coverage')
    expect(w.find('.btn-coverage').exists()).toBe(false)
  })
})
