/**
 * ExtensionPreferencesDialog — renders every control variant (including the
 * per-language enable/disable Switch), forwards a control change / toggle, and
 * reflects the per-row saving + save-error + enable/disable busy/error state.
 *
 * The real Dialog teleports its body through reka-ui's portal (which does not
 * render in happy-dom), so it is stubbed to render its default slot inline. That
 * keeps the assertions on the dialog's OWN behaviour — grouping, wiring the
 * controls, forwarding `change`/`toggle-enabled`, and the busy/error surfaces —
 * not on reka.
 *
 * The group header's enable/disable Switch and a preference's own Switch/
 * CheckBox control are both `[role="switch"]`, so tests select by aria-label
 * (the header's is "Enable <source> (<lang>)"; a preference's is its title)
 * rather than relying on DOM order.
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

function dataSaverSwitch(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.find('[aria-label="Data saver"]')
}

function groupEnableSwitch(wrapper: ReturnType<typeof mountDialog>) {
  return wrapper.find('[aria-label="Enable MangaDex (en)"]')
}

describe('ExtensionPreferencesDialog', () => {
  it('renders a control for every preference variant plus the group enable Switch', () => {
    const wrapper = mountDialog()
    expect(dataSaverSwitch(wrapper).exists()).toBe(true)
    expect(groupEnableSwitch(wrapper).exists()).toBe(true)
    expect(wrapper.find('select').exists()).toBe(true)
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(true)
    expect(wrapper.find('input[type="text"]').exists()).toBe(true)
  })

  it('forwards a control change (switch flip) with its write coordinates', async () => {
    const wrapper = mountDialog()
    await dataSaverSwitch(wrapper).trigger('click')
    const emitted = wrapper.emitted('change')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual({ sourceId: 'src-en', position: 0, value: false })
  })

  it('disables the row being written (savingKey)', () => {
    const wrapper = mountDialog({ savingKey: 'src-en:0' })
    // The Data saver switch (src-en:0) is disabled while its write is in flight.
    expect(dataSaverSwitch(wrapper).attributes('disabled')).toBeDefined()
  })

  it('surfaces a write failure banner (saveError)', () => {
    const wrapper = mountDialog({ saveError: 'Suwayomi rejected the change' })
    expect(wrapper.text()).toContain('Suwayomi rejected the change')
  })

  it('shows the empty state when there are no groups', () => {
    const wrapper = mountDialog({ groups: [] })
    expect(wrapper.text()).toContain('No configurable preferences')
  })

  it('renders the group enable Switch reflecting a disabled source', () => {
    const wrapper = mountDialog({ groups: [{ ...preferenceGroup, enabled: false }] })
    expect(groupEnableSwitch(wrapper).attributes('data-state')).toBe('unchecked')
  })

  it('forwards a toggle-enabled event with the new state', async () => {
    const wrapper = mountDialog()
    await groupEnableSwitch(wrapper).trigger('click')
    const emitted = wrapper.emitted('toggle-enabled')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual({ sourceId: 'src-en', enabled: false })
  })

  it('disables the group enable Switch while its toggle write is in flight (enablingKey)', () => {
    const wrapper = mountDialog({ enablingKey: 'src-en' })
    expect(groupEnableSwitch(wrapper).attributes('disabled')).toBeDefined()
  })

  it('surfaces an enable/disable write failure banner (enableError)', () => {
    const wrapper = mountDialog({ enableError: 'Suwayomi rejected the change' })
    expect(wrapper.text()).toContain('Suwayomi rejected the change')
  })
})
