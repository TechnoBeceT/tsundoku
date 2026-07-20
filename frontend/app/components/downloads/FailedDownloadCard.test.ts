/**
 * FailedDownloadCard — the honest failing-source render.
 *
 * Pins that a downloaded broken-upgrade row names the FAILING source (not the
 * satisfier) in the attempt badge, reads its Upgrade → target in the meta, shows
 * the failing source's own error + a labelled category, and labels the action
 * "Reset" only when the failing source is terminal.
 *
 * Non-vacuous: badge the satisfier (item.providerName) and the first assertion
 * fails ("Comix" not "Hive Scans"); read item.lastError instead of failingLastError
 * and the error assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FailedDownloadCard from './FailedDownloadCard.vue'
import type { DownloadItem } from '../screens/downloads.types'

// Stub the leaf visuals not under test; keep AttemptBadge real so we can read the
// N/max + source name it renders.
const stubs = { CoverImage: true, Chip: true, StatusBadge: true }

const upgradeFailure = (overrides: Partial<DownloadItem> = {}): DownloadItem => ({
  chapterId: 'c-1',
  seriesId: 's-1',
  seriesTitle: 'Solo Leveling',
  seriesCategory: 'Manhwa',
  coverUrl: '',
  number: 91,
  name: 'Chapter 91',
  state: 'downloaded',
  provider: 'comix-id',
  providerName: 'Comix',
  isUpgrade: true,
  upgradeTarget: 'Hive Scans',
  failingProvider: 'hive-id',
  failingProviderName: 'Hive Scans',
  failingAttempts: 3,
  maxRetries: 5,
  failingLastError: 'broken page: empty image response',
  failingErrorCategory: 'no_pages',
  retryable: true,
  terminal: false,
  ...overrides,
})

describe('FailedDownloadCard – honest failing source', () => {
  it('badges the FAILING source and its budget, not the satisfier', () => {
    const wrapper = mount(FailedDownloadCard, { props: { item: upgradeFailure() }, global: { stubs } })
    const badge = wrapper.find('.attempts')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toContain('Hive Scans')
    expect(badge.text()).toContain('3/5')
    expect(badge.text()).not.toContain('Comix')
  })

  it('reads the Upgrade → target in the meta line', () => {
    const wrapper = mount(FailedDownloadCard, { props: { item: upgradeFailure() }, global: { stubs } })
    const meta = wrapper.find('.dl-row__meta').text()
    expect(meta).toContain('Upgrade')
    expect(meta).toContain('→')
    expect(wrapper.find('.dl-row__target').text()).toBe('Hive Scans')
  })

  it('shows the failing source error + a labelled category, expandable', () => {
    const wrapper = mount(FailedDownloadCard, { props: { item: upgradeFailure(), expanded: true }, global: { stubs } })
    expect(wrapper.text()).toContain('broken page: empty image response')
    // no_pages → "No pages" label (the wider backend taxonomy, not ErrorCategory).
    expect(wrapper.find('.err-toggle__label').text()).toBe('No pages')
  })

  it('labels the action Retry while retryable and Reset when terminal', () => {
    const retry = mount(FailedDownloadCard, { props: { item: upgradeFailure() }, global: { stubs } })
    expect(retry.text()).toContain('Retry')

    const reset = mount(FailedDownloadCard, {
      props: { item: upgradeFailure({ failingAttempts: 5, retryable: false, terminal: true }) },
      global: { stubs },
    })
    expect(reset.text()).toContain('Reset')
  })

  it('emits retry with the chapter id when the button is clicked', async () => {
    const wrapper = mount(FailedDownloadCard, { props: { item: upgradeFailure() }, global: { stubs } })
    // The retry button is the AppButton; find by its label text.
    const buttons = wrapper.findAll('button')
    const retryBtn = buttons.find((b) => b.text().includes('Retry'))!
    await retryBtn.trigger('click')
    expect(wrapper.emitted('retry')?.[0]).toEqual(['c-1'])
  })

  it('falls back to the satisfier + chapter error for a plain state-failed row', () => {
    const wrapper = mount(FailedDownloadCard, {
      props: {
        item: {
          chapterId: 'c-9',
          seriesId: 's-1',
          seriesTitle: 'Berserk',
          seriesCategory: 'Manga',
          coverUrl: '',
          number: 365,
          name: 'Chapter 365',
          state: 'failed',
          provider: 'md-id',
          providerName: 'MangaDex',
          attempts: 2,
          maxRetries: 5,
          lastError: 'connection reset by peer',
          errorCategory: 'network',
        },
        expanded: true,
      },
      global: { stubs },
    })
    // No failing* fields → badge falls back to the satisfier's own attempts.
    expect(wrapper.find('.attempts').text()).toContain('MangaDex')
    expect(wrapper.find('.attempts').text()).toContain('2/5')
    expect(wrapper.text()).toContain('connection reset by peer')
    expect(wrapper.find('.err-toggle__label').text()).toBe('Network error')
  })
})
