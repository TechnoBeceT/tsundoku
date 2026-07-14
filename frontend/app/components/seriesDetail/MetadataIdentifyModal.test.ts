/**
 * MetadataIdentifyModal — the multi-select merge behaviour (QCAT-228): any
 * number of candidate cards may be picked, in CLICK ORDER, and `confirm`
 * emits exactly that array (index 0 = primary/anchor).
 *
 * The real Dialog teleports through reka-ui's portal (which does not render
 * in happy-dom), so it is stubbed to render its title + slots inline — the
 * same approach as RemoveSourceDialog.test.ts / MatchDiskProviderDialog.test.ts.
 *
 * Non-vacuous: each assertion pins a behaviour a naive single-select
 * (`selectedId = c.id`) implementation would fail — picking a second card
 * would replace, not add to, the selection.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import MetadataIdentifyModal from './MetadataIdentifyModal.vue'
import { metadataCandidates } from '../../fixtures/seriesDetail'

const DialogStub = {
  props: ['open', 'title'],
  template: '<div v-if="open" class="dialog-stub"><h2>{{ title }}</h2><slot /><slot name="actions" /></div>',
}

function mountModal(props: Record<string, unknown> = {}) {
  return mount(MetadataIdentifyModal, {
    props: {
      open: true,
      title: 'Chainsaw Man',
      candidates: metadataCandidates.slice(0, 3),
      ...props,
    },
    global: { stubs: { Dialog: DialogStub } },
  })
}

/** Finds the Nth candidate card's clickable button (cards are rendered as `<button class="cand">`). */
function cardButton(wrapper: ReturnType<typeof mountModal>, index: number) {
  return wrapper.findAll('button.cand')[index]!
}

describe('MetadataIdentifyModal — multi-select merge', () => {
  it('Confirm is disabled until at least one candidate is picked', () => {
    const wrapper = mountModal()

    const confirmBtn = wrapper.findAll('button').find((b) => b.text().includes('Confirm match'))!
    expect(confirmBtn.attributes('disabled')).toBeDefined()
  })

  it('picking a SECOND card adds to the selection instead of replacing it', async () => {
    const wrapper = mountModal()

    await cardButton(wrapper, 0).trigger('click')
    await cardButton(wrapper, 1).trigger('click')

    // Both cards now carry the selected treatment (aria-pressed="true").
    expect(cardButton(wrapper, 0).attributes('aria-pressed')).toBe('true')
    expect(cardButton(wrapper, 1).attributes('aria-pressed')).toBe('true')
  })

  it('confirm emits picks in CLICK ORDER (index 0 = primary), not grid order', async () => {
    const wrapper = mountModal()

    // Click the SECOND card first, then the FIRST — click order must win.
    await cardButton(wrapper, 1).trigger('click')
    await cardButton(wrapper, 0).trigger('click')

    const confirmBtn = wrapper.findAll('button').find((b) => b.text().includes('Merge 2 matches'))!
    await confirmBtn.trigger('click')

    const emitted = wrapper.emitted('confirm')
    expect(emitted).toHaveLength(1)
    const picks = emitted![0]![0] as { id: string }[]
    expect(picks.map((c) => c.id)).toEqual([
      metadataCandidates[1]!.id,
      metadataCandidates[0]!.id,
    ])
  })

  it('clicking an already-picked card deselects it (toggle off)', async () => {
    const wrapper = mountModal()

    await cardButton(wrapper, 0).trigger('click')
    expect(cardButton(wrapper, 0).attributes('aria-pressed')).toBe('true')

    await cardButton(wrapper, 0).trigger('click')
    expect(cardButton(wrapper, 0).attributes('aria-pressed')).toBe('false')
  })

  it('the Confirm label pluralizes once a second candidate is picked', async () => {
    const wrapper = mountModal()

    await cardButton(wrapper, 0).trigger('click')
    expect(wrapper.text()).toContain('Confirm match')

    await cardButton(wrapper, 1).trigger('click')
    expect(wrapper.text()).toContain('Merge 2 matches')
  })

  it('reopening the modal (open toggled true) clears any prior selection', async () => {
    const wrapper = mountModal()
    await cardButton(wrapper, 0).trigger('click')
    expect(cardButton(wrapper, 0).attributes('aria-pressed')).toBe('true')

    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })

    expect(cardButton(wrapper, 0).attributes('aria-pressed')).toBe('false')
  })
})
