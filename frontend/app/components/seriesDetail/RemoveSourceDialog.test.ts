/**
 * RemoveSourceDialog — the two §16 guarantees the shipped bug broke:
 *   1. the heading NEVER renders an empty quoted name (`Remove “”?`) — when the
 *      target source can't be resolved (it vanished from the list mid-confirm)
 *      the generic heading is used instead;
 *   2. a failed removal's reason is shown INSIDE the dialog, which stays open.
 *
 * The real Dialog teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed to render its title + slots inline — the same
 * approach as MatchDiskProviderDialog.test.ts.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import RemoveSourceDialog from './RemoveSourceDialog.vue'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(RemoveSourceDialog, {
    props: { open: true, sourceName: 'asurascans', ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

describe('RemoveSourceDialog', () => {
  it('names the source in the heading', () => {
    expect(mountDialog().text()).toContain('Remove “asurascans”?')
  })

  it('falls back to the generic heading when the source name cannot be resolved', () => {
    const wrapper = mountDialog({ sourceName: '' })

    expect(wrapper.text()).not.toContain('Remove “”?')
    expect(wrapper.text()).toContain('Remove this source?')
  })

  it('shows a failed removal’s reason inside the dialog (§16)', () => {
    expect(mountDialog({ error: 'Update failed' }).text()).toContain('Update failed')
  })

  it('emits confirm when the destructive confirm button is pressed', async () => {
    const wrapper = mountDialog()

    await wrapper.findAll('button').find((b) => b.text() === 'Remove source')!.trigger('click')

    expect(wrapper.emitted('confirm')).toHaveLength(1)
  })
})
