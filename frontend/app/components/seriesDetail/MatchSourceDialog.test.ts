/**
 * MatchSourceDialog — drives the search → pick → confirm flow and asserts the
 * emitted payloads, plus the §16 loading/error surfaces.
 *
 * The real Dialog teleports its body through reka-ui's portal (which does not
 * render in happy-dom), so it is stubbed to render its default + actions
 * slots inline (mirrors ExtensionPreferencesDialog.test.ts). That keeps the
 * assertions on the dialog's OWN behaviour — the two-stage flow, the emitted
 * search/confirm payloads, and the saving/error surfaces — not on reka.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import MatchSourceDialog from './MatchSourceDialog.vue'
import { searchResults } from '../../fixtures/import'

const DialogStub = { template: '<div class="dialog-stub"><slot /><slot name="actions" /></div>' }

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(MatchSourceDialog, {
    props: { open: true, seriesTitle: 'Solo Leveling', groups: searchResults, ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

describe('MatchSourceDialog', () => {
  it('prefills the search box with the series title', () => {
    const wrapper = mountDialog()
    const input = wrapper.find('input[type="search"]')
    expect((input.element as HTMLInputElement).value).toBe('Solo Leveling')
  })

  it('emits search with the trimmed query on Search click', async () => {
    const wrapper = mountDialog()
    await wrapper.find('input[type="search"]').setValue('  naruto  ')
    await wrapper.findAll('button').find(b => b.text() === 'Search')!.trigger('click')

    expect(wrapper.emitted('search')).toEqual([['naruto']])
  })

  it('advances to the pick stage and lists every candidate after picking a group', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    for (const candidate of searchResults[0]!.candidates) {
      expect(wrapper.text()).toContain(candidate.sourceName)
    }
  })

  it('toggling a candidate then confirming emits confirm with the chosen source/mangaId/importance', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    const target = searchResults[0]!.candidates[1]!
    await wrapper.find(`[aria-label="Toggle ${target.sourceName}"]`).trigger('click')
    await wrapper.find('input[type="number"]').setValue(7)
    await wrapper.findAll('button').find(b => b.text() === 'Attach source')!.trigger('click')

    expect(wrapper.emitted('confirm')).toEqual([[{ source: target.source, mangaId: target.mangaId, importance: 7 }]])
  })

  it('the Attach button stays disabled when the priority is a non-integer (must POST a clean int)', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    const target = searchResults[0]!.candidates[1]!
    await wrapper.find(`[aria-label="Toggle ${target.sourceName}"]`).trigger('click')
    await wrapper.find('input[type="number"]').setValue(1.5)

    const attach = wrapper.findAll('button').find(b => b.text() === 'Attach source')!
    expect(attach.attributes('disabled')).toBeDefined()

    // Clicking it anyway must not emit confirm with the decimal — no bad POST.
    await attach.trigger('click')
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('the Attach button stays disabled until a candidate is selected', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    const attach = wrapper.findAll('button').find(b => b.text() === 'Attach source')!
    expect(attach.attributes('disabled')).toBeDefined()
  })

  it('surfaces a search/add failure via the error banner', () => {
    const wrapper = mountDialog({ error: 'Suwayomi was unreachable' })
    expect(wrapper.text()).toContain('Suwayomi was unreachable')
  })

  it('disables the Attach button while saving (blocks a duplicate submit)', async () => {
    const wrapper = mountDialog({ saving: true })
    await wrapper.find('.group').trigger('click')

    const attach = wrapper.findAll('button').find(b => b.text() === 'Attach source')!
    expect(attach.attributes('disabled')).toBeDefined()
  })

  it('shows a "no matches" note after an empty search result', async () => {
    const wrapper = mountDialog({ groups: [] })
    await wrapper.findAll('button').find(b => b.text() === 'Search')!.trigger('click')

    expect(wrapper.text()).toContain('No matches found')
  })

  it('resets the flow (query, stage, selection) every time it re-opens', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')
    expect(wrapper.text()).toContain('Choose the source to attach')

    // Close then re-open — the stale "pick" stage must not survive.
    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })

    expect(wrapper.text()).not.toContain('Choose the source to attach')
    const input = wrapper.find('input[type="search"]')
    expect((input.element as HTMLInputElement).value).toBe('Solo Leveling')
  })
})
