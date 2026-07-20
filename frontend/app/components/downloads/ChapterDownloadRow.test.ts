/**
 * ChapterDownloadRow — the source line renders BOTH sides of an upgrade.
 *
 * Pins the convergence-wave affordance: a row carrying an `upgradeTarget` reads
 * "<current> → <target>" (the source the chapter is being upgraded TO), while a row
 * without one shows only its current source and no arrow.
 *
 * Non-vacuous: drop the `v-if="item.upgradeTarget"` block from the template and the
 * first assertion fails (no target text, no arrow); render the target
 * unconditionally and the second fails (an arrow on a plain downloading row).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ChapterDownloadRow from './ChapterDownloadRow.vue'
import type { DownloadItem } from '../screens/downloads.types'

// The child atoms are exercised by their own stories/tests — stub them so this test
// asserts only the row's own source line.
const stubs = { CoverImage: true, Chip: true, StatusBadge: true, AttemptBadge: true }

const item = (overrides: Partial<DownloadItem> = {}): DownloadItem => ({
  chapterId: 'c-1',
  seriesId: 's-1',
  seriesTitle: 'Berserk',
  seriesCategory: 'Manga',
  coverUrl: '',
  number: 365,
  name: 'The Flower of the Stone Castle',
  state: 'upgrading',
  provider: '2499283573021220255',
  providerName: 'MangaDex',
  ...overrides,
})

describe('ChapterDownloadRow', () => {
  it('renders "current → target" when the chapter is upgrading', () => {
    const wrapper = mount(ChapterDownloadRow, {
      props: { item: item({ upgradeTarget: 'Asura Scans' }) },
      global: { stubs },
    })

    const meta = wrapper.find('.dl-row__meta').text()
    expect(meta).toContain('MangaDex')
    expect(meta).toContain('→')
    expect(wrapper.find('.dl-row__target').text()).toBe('Asura Scans')
  })

  it('renders only the current source when there is no upgrade target', () => {
    const wrapper = mount(ChapterDownloadRow, {
      props: { item: item({ state: 'downloading', upgradeTarget: undefined }) },
      global: { stubs },
    })

    const meta = wrapper.find('.dl-row__meta').text()
    expect(meta).toContain('MangaDex')
    expect(meta).not.toContain('→')
    expect(wrapper.find('.dl-row__target').exists()).toBe(false)
  })

  it('marks an upgrade row with an "Upgrade" label and its target', () => {
    const wrapper = mount(ChapterDownloadRow, {
      props: { item: item({ state: 'upgrade_available', isUpgrade: true, upgradeTarget: 'Asura Scans' }) },
      global: { stubs },
    })
    const meta = wrapper.find('.dl-row__meta').text()
    expect(meta).toContain('Upgrade')
    expect(meta).toContain('→')
    expect(wrapper.find('.dl-row__target').text()).toBe('Asura Scans')
  })

  it('marks a targetless upgrade as an upgrade to a generic destination', () => {
    const wrapper = mount(ChapterDownloadRow, {
      props: { item: item({ state: 'upgrade_available', isUpgrade: true, upgradeTarget: undefined }) },
      global: { stubs },
    })
    const meta = wrapper.find('.dl-row__meta').text()
    expect(meta).toContain('Upgrade')
    expect(meta).toContain('higher source')
  })

  it('renders the per-source attempt badge when a retry budget is known', () => {
    const wrapper = mount(ChapterDownloadRow, {
      props: { item: item({ state: 'downloading', maxRetries: 3, attempts: 1 }) },
      // Do NOT stub AttemptBadge here so we can prove it is rendered.
      global: { stubs: { CoverImage: true, Chip: true, StatusBadge: true } },
    })
    // AttemptBadge's root carries `.attempts`; its N/max reads "1/3".
    expect(wrapper.find('.attempts').exists()).toBe(true)
    expect(wrapper.find('.attempts').text()).toContain('1/3')
  })

  // The backend reports an EMPTY providerName when no source's feed carries the
  // chapter — nothing is fetching it. The row must say so with an em-dash, not
  // leave a dangling "· " separator.
  it('renders an em-dash when no source is fetching the chapter', () => {
    const wrapper = mount(ChapterDownloadRow, {
      props: { item: item({ state: 'wanted', provider: '', providerName: '', upgradeTarget: undefined }) },
      global: { stubs },
    })

    expect(wrapper.find('.dl-row__provider').text()).toContain('—')
  })
})
