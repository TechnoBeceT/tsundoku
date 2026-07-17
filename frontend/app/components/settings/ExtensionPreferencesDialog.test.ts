/**
 * ExtensionPreferencesDialog — renders every control variant, forwards a
 * control change, reflects the per-row saving/save-error state, AND drives the
 * per-language enable/disable Switch: forwarding `toggle-enabled`, collapsing a
 * disabled group's preference block, and reflecting the enabling/enable-error
 * state.
 *
 * The real Dialog teleports its body through reka-ui's portal (which does not
 * render in happy-dom), so it is stubbed to render its default slot inline. That
 * keeps the assertions on the dialog's OWN behaviour — grouping, wiring the
 * controls, forwarding events, and the busy/error surfaces — not on reka.
 *
 * A preference's own Switch/CheckBox control is `[role="switch"]`, so tests
 * select by aria-label (a preference's is its title, a group's enable Switch is
 * `Enable <source> (<lang>)`) rather than relying on DOM order.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import ExtensionPreferencesDialog from './ExtensionPreferencesDialog.vue'
import { preferenceGroup, preferenceGroups } from '../../fixtures/preferences'

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

describe('ExtensionPreferencesDialog', () => {
  it('renders a control for every preference variant', () => {
    const wrapper = mountDialog()
    expect(dataSaverSwitch(wrapper).exists()).toBe(true)
    expect(wrapper.find('select').exists()).toBe(true)
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(true)
    expect(wrapper.find('input[type="text"]').exists()).toBe(true)
  })

  it('forwards a control change (switch flip) with its write coordinates', async () => {
    const wrapper = mountDialog()
    await dataSaverSwitch(wrapper).trigger('click')
    const emitted = wrapper.emitted('change')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual({ sourceId: 'src-en', key: 'dataSaver_en', value: false })
  })

  it('disables the row being written (savingKey)', () => {
    const wrapper = mountDialog({ savingKey: 'src-en:dataSaver_en' })
    // The Data saver switch (src-en:dataSaver_en) is disabled while its write is in flight.
    expect(dataSaverSwitch(wrapper).attributes('disabled')).toBeDefined()
  })

  it('surfaces a write failure banner (saveError)', () => {
    const wrapper = mountDialog({ saveError: 'The engine host rejected the change' })
    expect(wrapper.text()).toContain('The engine host rejected the change')
  })

  it('shows the empty state when there are no groups', () => {
    const wrapper = mountDialog({ groups: [] })
    expect(wrapper.text()).toContain('No configurable preferences')
  })

  // ---- per-language enable/disable Switch (feature #1 + #2) ------------------

  it('forwards toggle-enabled when a group Switch is flipped', async () => {
    const wrapper = mountDialog() // preferenceGroup is enabled
    await wrapper.find('[aria-label="Enable MangaDex (en)"]').trigger('click')
    const emitted = wrapper.emitted('toggle-enabled')
    expect(emitted).toBeTruthy()
    // An enabled source flips to disabled.
    expect(emitted![0]![0]).toEqual({ sourceId: 'src-en', enabled: false })
  })

  it('collapses a disabled group\'s preference block, keeping the enabled group\'s controls', () => {
    // preferenceGroups = [en enabled (all variants), ja DISABLED (one switch)].
    const wrapper = mountDialog({ groups: preferenceGroups })
    // The disabled JA group shows the collapsed note, not its switch control.
    expect(wrapper.text()).toContain('Disabled — hidden from Discover')
    // The enabled EN group's Data saver control still renders.
    expect(dataSaverSwitch(wrapper).exists()).toBe(true)
    // The JA group's enable Switch itself is still present (so it can be re-enabled).
    expect(wrapper.find('[aria-label="Enable MangaDex (ja)"]').exists()).toBe(true)
  })

  it('disables the group Switch being written (enablingKey)', () => {
    const wrapper = mountDialog({ enablingKey: 'src-en' })
    expect(wrapper.find('[aria-label="Enable MangaDex (en)"]').attributes('disabled')).toBeDefined()
  })

  it('surfaces an enable/disable failure banner (enableError)', () => {
    const wrapper = mountDialog({ enableError: 'Failed to update source' })
    expect(wrapper.text()).toContain('Failed to update source')
  })
})
