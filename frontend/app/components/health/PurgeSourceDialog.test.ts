/**
 * PurgeSourceDialog — the §16 guarantees + the preview surface:
 *   1. the heading NEVER renders an empty quoted name (`Purge “”?`) — the generic
 *      heading is used when the source name cannot be resolved;
 *   2. the dry-run preview counts render (so the owner sees the blast radius);
 *   3. a failed purge's reason shows INSIDE the dialog, which stays open;
 *   4. confirm is emitted from the destructive confirm button.
 *
 * The real Dialog teleports through reka-ui's portal (which does not render in
 * happy-dom), so it is stubbed to render its title + slots inline — mirroring
 * RemoveSourceDialog.test.ts.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import PurgeSourceDialog from './PurgeSourceDialog.vue'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

const preview = {
  sourceId: '100',
  sourceName: 'Lunar Manga',
  seriesAffected: 3,
  providers: 3,
  providerChapters: 240,
  chaptersDeleted: 2,
  metrics: 1,
  breaker: 1,
}

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(PurgeSourceDialog, {
    props: { open: true, sourceName: 'Lunar Manga', ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

describe('PurgeSourceDialog', () => {
  it('names the source in the heading', () => {
    expect(mountDialog().text()).toContain('Purge “Lunar Manga”?')
  })

  it('falls back to the generic heading when the source name cannot be resolved', () => {
    const wrapper = mountDialog({ sourceName: '' })

    expect(wrapper.text()).not.toContain('Purge “”?')
    expect(wrapper.text()).toContain('Purge this source?')
  })

  it('renders the dry-run preview counts', () => {
    const text = mountDialog({ preview }).text()
    expect(text).toContain('3 series affected')
    expect(text).toContain('240 tracked chapter')
    expect(text).toContain('2 orphaned chapter')
  })

  it('shows a failed purge’s reason inside the dialog (§16)', () => {
    expect(mountDialog({ preview, error: 'Purge failed' }).text()).toContain('Purge failed')
  })

  it('emits confirm when the destructive confirm button is pressed', async () => {
    const wrapper = mountDialog({ preview })

    await wrapper.findAll('button').find((b) => b.text() === 'Purge source')!.trigger('click')

    expect(wrapper.emitted('confirm')).toHaveLength(1)
  })
})
