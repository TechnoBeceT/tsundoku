/**
 * Downloads (Queued tab) — the DeferralNote stands down on a WITHHELD row.
 *
 * A chapter the source is holding behind early access (GAP-141) carries a future
 * `deferredUntil` exactly like a backed-off one, so the deferral note's guard is
 * the only thing separating them. Without `&& !row.locked` the row renders BOTH
 * the EarlyAccessBadge ("Early access · free ~3d") and a "retrying ~3d" deferral
 * line directly beside it — two different explanations for the same wait, one of
 * which frames a healthy paywall as a fault the owner should chase.
 *
 * Non-vacuous by construction: the two rows differ ONLY in `locked`, so dropping
 * `&& !row.locked` makes the first test fail while the second still passes, and
 * dropping the whole `v-if` makes the second fail.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Downloads from './Downloads.vue'
import DeferralNote from '../downloads/DeferralNote.vue'
import type { DownloadItem } from './downloads.types'

/** A future ISO instant N hours out — both rows' cooldowns must be live, not elapsed. */
const inHours = (n: number): string => new Date(Date.now() + n * 3_600_000).toISOString()

/**
 * One queued row waiting on a future source cooldown. `locked` is the only field
 * the two tests vary, so the assertions can only be explained by the guard.
 */
const queuedRow = (overrides: Partial<DownloadItem> = {}): DownloadItem => ({
  chapterId: 'c-1',
  seriesId: 's-1',
  seriesTitle: 'Solo Leveling',
  seriesCategory: 'Manhwa',
  coverUrl: '',
  number: 200,
  name: 'Chapter 200',
  state: 'upgrade_available',
  provider: '2528143451863530665',
  providerName: 'Hive Scans',
  deferredUntil: inHours(72),
  waitingReason: 'backoff',
  ...overrides,
})

/** Mounts the screen on the Queued tab with a single row. */
const mountQueued = (item: DownloadItem) =>
  mount(Downloads, {
    props: { items: [item], activeTab: 'queued' },
    global: { stubs: { CoverImage: true, Chip: true } },
  })

describe('Downloads — queued deferral note', () => {
  it('suppresses the deferral note on an early-access (locked) row', () => {
    const wrapper = mountQueued(queuedRow({ locked: true, lockedUntil: inHours(72) }))

    expect(wrapper.findComponent(DeferralNote).exists()).toBe(false)
    // The badge is what explains the wait instead — proving the row really is the
    // withheld one and the note was suppressed, not merely absent.
    expect(wrapper.text()).toContain('Early access')
  })

  it('still renders the deferral note on a deferred row that is not locked', () => {
    const wrapper = mountQueued(queuedRow())

    expect(wrapper.findComponent(DeferralNote).exists()).toBe(true)
  })
})
