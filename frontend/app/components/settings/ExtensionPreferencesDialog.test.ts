/**
 * ExtensionPreferencesDialog — renders every control variant, forwards a control
 * change, and reflects the per-row saving + save-error state.
 *
 * The real Dialog teleports its body through reka-ui's portal (which does not
 * render in happy-dom), so it is stubbed to render its default slot inline. That
 * keeps the assertions on the dialog's OWN behaviour — grouping, wiring the four
 * controls, forwarding `change`, and the busy/error surfaces — not on reka.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ExtensionPreferencesDialog from './ExtensionPreferencesDialog.vue'
import { preferenceGroup } from '../../fixtures/preferences'

// Stub reka's Dialog to render its default slot inline (no portal/teleport).
const DialogStub = { template: '<div class="dialog-stub"><slot /></div>' }

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(ExtensionPreferencesDialog, {
    props: { open: true, extensionName: 'MangaDex', groups: [preferenceGroup], ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

describe('ExtensionPreferencesDialog', () => {
  it('renders a control for every preference variant', () => {
    const wrapper = mountDialog()
    expect(wrapper.find('[role="switch"]').exists()).toBe(true)
    expect(wrapper.find('select').exists()).toBe(true)
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(true)
    expect(wrapper.find('input[type="text"]').exists()).toBe(true)
  })

  it('forwards a control change (switch flip) with its write coordinates', async () => {
    const wrapper = mountDialog()
    await wrapper.find('[role="switch"]').trigger('click')
    const emitted = wrapper.emitted('change')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual({ sourceId: 'src-en', position: 0, value: false })
  })

  it('disables the row being written (savingKey)', () => {
    const wrapper = mountDialog({ savingKey: 'src-en:0' })
    // The Data saver switch (src-en:0) is disabled while its write is in flight.
    expect(wrapper.find('[role="switch"]').attributes('disabled')).toBeDefined()
  })

  it('surfaces a write failure banner (saveError)', () => {
    const wrapper = mountDialog({ saveError: 'Suwayomi rejected the change' })
    expect(wrapper.text()).toContain('Suwayomi rejected the change')
  })

  it('shows the empty state when there are no groups', () => {
    const wrapper = mountDialog({ groups: [] })
    expect(wrapper.text()).toContain('No configurable preferences')
  })
})
