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
  fractionalCount: 0,
  fractionalChapters: [],
  ignoreFractional: false,
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

/**
 * The fractional evidence + the ignore switch.
 *
 * The owner refused a blind toggle: he must SEE which fractional chapters a
 * source carries before suppressing them, because nothing else distinguishes a
 * mirror that re-uploads whole chapters as "N.1" from a source carrying a
 * genuine `5.5` omake (an automatic rule would have destroyed 825 real `.5`
 * chapters in his library). So: the LIST is rendered, never hidden — and where
 * there is no evidence there is no switch.
 */
describe('ProviderRow — fractional evidence + ignore toggle', () => {
  const reuploader = {
    fractionalCount: 9,
    fractionalChapters: ['1.1', '2.1', '3.1', '4.1', '5.1', '6.1', '7.1', '8.1', '9.1'],
  }

  it('lists a re-uploader\'s systematic run beside the count', () => {
    const w = render(reuploader)

    expect(w.find('.source__fractional-count').text()).toBe('9 fractional')
    expect(w.find('.source__fractional-list').text()).toBe('1.1, 2.1, 3.1, 4.1, 5.1, 6.1, 7.1, 8.1, 9.1')
  })

  it('lists a lone .5 omake — the chapter the owner must NOT suppress', () => {
    const w = render({ fractionalCount: 1, fractionalChapters: ['5.5'] })

    expect(w.find('.source__fractional-count').text()).toBe('1 fractional')
    expect(w.find('.source__fractional-list').text()).toBe('5.5')
  })

  it('caps the list at 12 with "+N more" while the COUNT still reports the true total', () => {
    const chapters = Array.from({ length: 30 }, (_, i) => `${i + 1}.1`)
    const w = render({ fractionalCount: 30, fractionalChapters: chapters })

    const list = w.find('.source__fractional-list').text()
    expect(list).toContain('12.1 +18 more')
    expect(list).not.toContain('13.1')
    expect(w.find('.source__fractional-count').text()).toBe('30 fractional')
  })

  it('emits toggleIgnoreFractional(true) when the switch is turned on', async () => {
    const w = render(reuploader)

    await w.find('[role="switch"]').trigger('click')

    expect(w.emitted('toggleIgnoreFractional')).toEqual([[true]])
  })

  it('emits toggleIgnoreFractional(false) when an ignored source is un-ticked', async () => {
    const w = render({ ...reuploader, ignoreFractional: true })

    await w.find('[role="switch"]').trigger('click')

    expect(w.emitted('toggleIgnoreFractional')).toEqual([[false]])
  })

  it('keeps the evidence visible while ignored — the decision stays auditable', () => {
    const w = render({ ...reuploader, ignoreFractional: true })

    expect(w.find('.source__fractional-list').text()).toContain('1.1')
    expect(w.find('[role="switch"]').attributes('aria-checked')).toBe('true')
  })

  it('renders NO fractional line and NO toggle when the source has no fractionals', () => {
    const w = render({ fractionalCount: 0, fractionalChapters: [] })

    expect(w.find('.source__fractional').exists()).toBe(false)
    expect(w.find('.source__fractional-toggle').exists()).toBe(false)
    expect(w.find('[role="switch"]').exists()).toBe(false)
    expect(w.text()).not.toContain('Ignore fractional chapters')
  })

  it('disables the switch while a mutation is in flight', () => {
    const w = mount(ProviderRow, {
      props: {
        provider: { ...base, ...reuploader },
        rank: 1,
        preferred: true,
        canUp: false,
        canDown: true,
        saving: true,
      },
    })

    expect(w.find('[role="switch"]').attributes('disabled')).toBeDefined()
  })
})
