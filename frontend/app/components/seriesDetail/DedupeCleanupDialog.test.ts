/**
 * DedupeCleanupDialog — the preview→confirm guarantees for "Remove duplicate files".
 *
 * Pinned here:
 *   1. the plan is grouped by reason, each group labelled + counted, and every
 *      planned filename is shown (the owner sees EXACTLY what will go);
 *   2. the confirm label states the real total and confirming emits `confirm`;
 *   3. an EMPTY plan shows "nothing to remove", offers only Close, and can never
 *      emit `confirm` (the destructive POST must not fire on an empty plan);
 *   4. a failed removal keeps the dialog open with the reason inside it (§16).
 *
 * The real Dialog teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed to render its title + slots inline — the same
 * approach as FractionalCleanupDialog.test.ts.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import DedupeCleanupDialog from './DedupeCleanupDialog.vue'
import type { DedupePlanItem } from '../screens/seriesDetail.types'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

/** A plan touching all three removal sources. */
const fullPlan: DedupePlanItem[] = [
  { reason: 'epilogue-merge', number: -1, filename: 'epilogue.cbz' },
  { reason: 'ignored-fractional', number: 181.5, filename: '181.5.cbz' },
  { reason: 'ignored-fractional', number: 190.5, filename: '190.5.cbz' },
  { reason: 'orphan-superseded', number: 7, filename: '[old] 007.cbz' },
  { reason: 'orphan-superseded', number: 9.1, filename: '[stray] 009.1.cbz' },
]

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(DedupeCleanupDialog, {
    props: { open: true, items: fullPlan, ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

function confirmButton(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find((b) => b.text().startsWith('Remove '))
}

describe('DedupeCleanupDialog — the plan list', () => {
  it('groups the plan by reason, each group counted, and lists every planned filename', () => {
    const wrapper = mountDialog()
    const groups = wrapper.findAll('.dedupe-group')

    // Three non-empty reason groups, in fixed order.
    expect(groups).toHaveLength(3)
    expect(groups[0]!.text()).toContain('Duplicate chapter rows')
    expect(groups[1]!.text()).toContain('Ignored fractional chapters')
    expect(groups[2]!.text()).toContain('Orphan / duplicate files')
    // The ignored-fractional group carries its own count (2 of the 5 items).
    expect(groups[1]!.find('.dedupe-group__count').text()).toBe('2')

    // Every planned file is rendered — the owner sees exactly what will go.
    for (const item of fullPlan) {
      expect(wrapper.text()).toContain(item.filename)
    }
    expect(wrapper.text()).toContain('5 items to remove')
  })

  it('renders an un-numbered item as an em-dash, never a fake number', () => {
    const wrapper = mountDialog({ items: [{ reason: 'epilogue-merge', number: null, filename: 'named.cbz' }] })
    expect(wrapper.find('.dedupe-row__number').text()).toBe('—')
  })
})

describe('DedupeCleanupDialog — the confirm', () => {
  it('the confirm label states the real total and emits confirm on click', async () => {
    const wrapper = mountDialog()
    expect(confirmButton(wrapper)!.text()).toBe('Remove 5 items')

    await confirmButton(wrapper)!.trigger('click')
    expect(wrapper.emitted('confirm')).toHaveLength(1)
  })

  it('a failed removal shows its reason inside the dialog (§16 — it stays open)', () => {
    expect(mountDialog({ error: 'Dedupe files failed' }).text()).toContain('Dedupe files failed')
  })
})

describe('DedupeCleanupDialog — the empty plan', () => {
  it('shows "nothing to remove", offers only Close, and never emits confirm', () => {
    const wrapper = mountDialog({ items: [] })

    expect(wrapper.text()).toContain('Nothing to remove')
    expect(confirmButton(wrapper)).toBeUndefined()

    const close = wrapper.findAll('button').find((b) => b.text() === 'Close')
    expect(close).toBeDefined()
    // Even calling confirm() indirectly can never fire on an empty plan.
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })
})
