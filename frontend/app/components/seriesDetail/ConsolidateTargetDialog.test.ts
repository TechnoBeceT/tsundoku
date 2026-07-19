/**
 * ConsolidateTargetDialog — the multi-provider consolidation target picker
 * (QCAT-295 Part B). Drives the radio-group pick and asserts the emitted payload:
 * an existing provider → `confirm(id)`; "Match to a new source" → `matchToSource`.
 * The real Dialog teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed to render its default + actions slots inline
 * (mirrors MatchDiskProviderDialog.test.ts).
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ConsolidateTargetDialog from './ConsolidateTargetDialog.vue'

const DialogStub = { template: '<div class="dialog-stub"><slot /><slot name="actions" /></div>' }

const candidates = [
  { id: 'p-real', name: 'QiScans' },
  { id: 'p-other', name: 'Asura Scans' },
]

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(ConsolidateTargetDialog, {
    props: { open: true, selectedCount: 3, candidates, ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

// The Merge button is the confirm action (the only non-Cancel footer button).
function mergeButton(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.findAll('button').find(b => b.text().includes('Merge'))!
}

describe('ConsolidateTargetDialog', () => {
  it('shows the folded-count copy and one radio per candidate + the match option', () => {
    const wrapper = mountDialog()
    expect(wrapper.text()).toContain('3 selected sources')
    expect(wrapper.text()).toContain('QiScans')
    expect(wrapper.text()).toContain('Asura Scans')
    expect(wrapper.text()).toContain('Match to a new source')
    // 2 candidates + the match sentinel = 3 radios.
    expect(wrapper.findAll('input[type="radio"]')).toHaveLength(3)
  })

  it('disables Merge until a survivor is chosen', async () => {
    const wrapper = mountDialog()
    expect(mergeButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.findAll('input[type="radio"]')[0]!.setValue()
    expect(mergeButton(wrapper).attributes('disabled')).toBeUndefined()
  })

  it('emits confirm with the chosen EXISTING provider id', async () => {
    const wrapper = mountDialog()
    await wrapper.findAll('input[type="radio"]')[0]!.setValue() // QiScans (p-real)
    await mergeButton(wrapper).trigger('click')
    expect(wrapper.emitted('confirm')).toEqual([['p-real']])
    expect(wrapper.emitted('matchToSource')).toBeUndefined()
  })

  it('emits matchToSource (not confirm) when the match option is chosen', async () => {
    const wrapper = mountDialog()
    await wrapper.findAll('input[type="radio"]')[2]!.setValue() // the match sentinel
    await mergeButton(wrapper).trigger('click')
    expect(wrapper.emitted('matchToSource')).toHaveLength(1)
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('with no candidates offers only the match option + a note', () => {
    const wrapper = mountDialog({ candidates: [] })
    expect(wrapper.findAll('input[type="radio"]')).toHaveLength(1)
    expect(wrapper.text()).toContain('No other source on this series')
  })

  it('shows a failed-merge error inside the dialog (§16)', () => {
    const wrapper = mountDialog({ error: 'Merge failed' })
    expect(wrapper.text()).toContain('Merge failed')
  })
})
