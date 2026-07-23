/**
 * Sourceless screen — the smart-component wiring around `useSourceless()` +
 * the reused `SourcelessCleanupDialog`.
 *
 * Pins:
 *   1. an empty list renders the all-clear EmptyState (never a bare list);
 *   2. a non-empty list renders one SourcelessSeriesCard per series;
 *   3. clicking a card's "Review" fetches that series' preview and opens the
 *      dialog with it;
 *   4. a successful removal re-polls the list (via the composable) and closes
 *      the dialog;
 *   5. a FAILED removal keeps the dialog open (§16 — never silently closed).
 *
 * `useSourceless` is mocked (mirrors the reader-route test's composable-mock
 * pattern) so this test drives the screen's OWN wiring, not the network layer
 * already pinned by `useSourceless.test.ts`. Mounts the REAL child components
 * (mirrors Fractionals.test.ts).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ref } from 'vue'
import { mount } from '@vue/test-utils'
import Sourceless from './Sourceless.vue'
import SourcelessSeriesCard from '../sourceless/SourcelessSeriesCard.vue'
import SourcelessCleanupDialog from '../seriesDetail/SourcelessCleanupDialog.vue'
import { sampleSourcelessSeries, sampleSourcelessPreview } from '../../fixtures/sourceless'

const series = ref([...sampleSourcelessSeries])
const pending = ref(false)
const refreshing = ref(false)
const error = ref<string | null>(null)
const removeBusy = ref(false)
const removeError = ref<string | null>(null)
const refresh = vi.fn()
const fetchPreview = vi.fn()
const removeSourceless = vi.fn()

vi.mock('../../composables/useSourceless', () => ({
  useSourceless: () => ({
    series,
    pending,
    refreshing,
    error,
    removeBusy,
    removeError,
    refresh,
    fetchPreview,
    removeSourceless,
  }),
}))

beforeEach(() => {
  series.value = [...sampleSourcelessSeries]
  pending.value = false
  refreshing.value = false
  error.value = null
  removeBusy.value = false
  removeError.value = null
  refresh.mockReset()
  fetchPreview.mockReset().mockResolvedValue(sampleSourcelessPreview)
  removeSourceless.mockReset().mockResolvedValue(true)
})

describe('Sourceless screen', () => {
  it('shows the all-clear empty state when there are no series', () => {
    series.value = []
    const wrapper = mount(Sourceless)
    expect(wrapper.text()).toContain('Nothing sourceless')
    expect(wrapper.findAllComponents(SourcelessSeriesCard)).toHaveLength(0)
  })

  it('renders one card per series', () => {
    const wrapper = mount(Sourceless)
    expect(wrapper.findAllComponents(SourcelessSeriesCard)).toHaveLength(sampleSourcelessSeries.length)
  })

  it('clicking "Review" fetches the preview and opens the dialog with it', async () => {
    const wrapper = mount(Sourceless)
    const firstCard = wrapper.findComponent(SourcelessSeriesCard)
    firstCard.vm.$emit('review', sampleSourcelessSeries[0]!.seriesId)
    await flushPromises()

    expect(fetchPreview).toHaveBeenCalledWith(sampleSourcelessSeries[0]!.seriesId)
    const dialog = wrapper.findComponent(SourcelessCleanupDialog)
    expect(dialog.props('open')).toBe(true)
    expect(dialog.props('preview')).toEqual(sampleSourcelessPreview)
  })

  it('a successful removal calls removeSourceless and closes the dialog', async () => {
    const wrapper = mount(Sourceless)
    const firstCard = wrapper.findComponent(SourcelessSeriesCard)
    firstCard.vm.$emit('review', sampleSourcelessSeries[0]!.seriesId)
    await flushPromises()

    const dialog = wrapper.findComponent(SourcelessCleanupDialog)
    dialog.vm.$emit('confirm', ['c-1'])
    await flushPromises()

    expect(removeSourceless).toHaveBeenCalledWith(sampleSourcelessSeries[0]!.seriesId, ['c-1'])
    expect(wrapper.findComponent(SourcelessCleanupDialog).props('open')).toBe(false)
  })

  it('a failed removal keeps the dialog open with the error shown inside it (§16)', async () => {
    removeSourceless.mockResolvedValue(false)
    removeError.value = 'boom'
    const wrapper = mount(Sourceless)
    const firstCard = wrapper.findComponent(SourcelessSeriesCard)
    firstCard.vm.$emit('review', sampleSourcelessSeries[0]!.seriesId)
    await flushPromises()

    const dialog = wrapper.findComponent(SourcelessCleanupDialog)
    dialog.vm.$emit('confirm', ['c-1'])
    await flushPromises()

    expect(wrapper.findComponent(SourcelessCleanupDialog).props('open')).toBe(true)
    expect(wrapper.findComponent(SourcelessCleanupDialog).props('error')).toBe('boom')
  })
})

/** Flush pending microtasks (the async onReview/onConfirm handlers) + a DOM tick. */
async function flushPromises(): Promise<void> {
  await Promise.resolve()
  await Promise.resolve()
}
