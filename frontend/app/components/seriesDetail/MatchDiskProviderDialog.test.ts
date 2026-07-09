/**
 * MatchDiskProviderDialog — drives the search → pick source → pick scanlator
 * → confirm flow and asserts the emitted payloads, plus the §16 loading/error
 * surfaces. Mirrors `MatchSourceDialog.test.ts`'s stubbing approach: the real
 * Dialog teleports its body through reka-ui's portal (which does not render
 * in happy-dom), so it is stubbed to render its default + actions slots
 * inline, keeping assertions on this dialog's OWN behaviour.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import MatchDiskProviderDialog from './MatchDiskProviderDialog.vue'
import { searchResults, scanlatorBreakdown } from '../../fixtures/import'
import type { ScanlatorCoverage } from '../screens/import.types'

const DialogStub = { template: '<div class="dialog-stub"><slot /><slot name="actions" /></div>' }

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(MatchDiskProviderDialog, {
    props: {
      open: true,
      seriesTitle: 'Solo Leveling',
      providerLabel: 'Unknown (imported)',
      chapterCount: 45,
      defaultImportance: 1,
      groups: searchResults,
      ...props,
    },
    global: { stubs: { Dialog: DialogStub } },
  })
}

const firstCandidate = searchResults[0]!.candidates[0]!
const secondCandidate = searchResults[0]!.candidates[1]!

async function pickGroupAndCandidate(wrapper: ReturnType<typeof mountDialog>) {
  await wrapper.find('.group').trigger('click')
  await wrapper.find(`[aria-label="Toggle ${firstCandidate.sourceName}"]`).trigger('click')
}

describe('MatchDiskProviderDialog', () => {
  it('prefills the search box with the series title and shows the no-re-download copy', () => {
    const wrapper = mountDialog()
    const input = wrapper.find('input[type="search"]')
    expect((input.element as HTMLInputElement).value).toBe('Solo Leveling')
    expect(wrapper.text()).toContain('Link these')
    expect(wrapper.text()).toContain('45')
    expect(wrapper.text()).toContain('Unknown (imported)')
    expect(wrapper.text()).toContain('no re-download')
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

  it('selecting a candidate emits pickCandidate with its source and mangaId', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')
    await wrapper.find(`[aria-label="Toggle ${firstCandidate.sourceName}"]`).trigger('click')

    expect(wrapper.emitted('pickCandidate')).toEqual([[{ source: firstCandidate.source, mangaId: firstCandidate.mangaId }]])
  })

  it('deselecting the chosen candidate hides the breakdown section again', async () => {
    const wrapper = mountDialog({ breakdown: scanlatorBreakdown })
    await pickGroupAndCandidate(wrapper)
    expect(wrapper.text()).toContain('Pick the scanlation group')

    await wrapper.find(`[aria-label="Toggle ${firstCandidate.sourceName}"]`).trigger('click')
    expect(wrapper.text()).not.toContain('Pick the scanlation group')
  })

  it('switching to a different candidate re-emits pickCandidate for the new one', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')
    await wrapper.find(`[aria-label="Toggle ${firstCandidate.sourceName}"]`).trigger('click')
    await wrapper.find(`[aria-label="Toggle ${secondCandidate.sourceName}"]`).trigger('click')

    expect(wrapper.emitted('pickCandidate')).toEqual([
      [{ source: firstCandidate.source, mangaId: firstCandidate.mangaId }],
      [{ source: secondCandidate.source, mangaId: secondCandidate.mangaId }],
    ])
  })

  it('shows a loading state while the breakdown is in flight', async () => {
    const wrapper = mountDialog({ breakdownLoading: true })
    await pickGroupAndCandidate(wrapper)

    expect(wrapper.text()).toContain('Loading chapter breakdown')
  })

  it('lists each scanlator with its coverage once the breakdown resolves', async () => {
    const wrapper = mountDialog({ breakdown: scanlatorBreakdown })
    await pickGroupAndCandidate(wrapper)

    for (const sc of scanlatorBreakdown) {
      expect(wrapper.text()).toContain(sc.scanlator)
      expect(wrapper.text()).toContain(sc.ranges)
    }
  })

  it('offers a "coverage unavailable" fallback when the breakdown failed to load', async () => {
    const wrapper = mountDialog({ breakdown: null })
    await pickGroupAndCandidate(wrapper)

    expect(wrapper.text()).toContain('Coverage unavailable')
  })

  it('picking a scanlator, then confirming, emits confirm with source/mangaId/scanlator/importance', async () => {
    const wrapper = mountDialog({ breakdown: scanlatorBreakdown })
    await pickGroupAndCandidate(wrapper)

    await wrapper.find(`[aria-label="Toggle ${scanlatorBreakdown[0]!.scanlator}"]`).trigger('click')
    await wrapper.find('input[type="number"]').setValue(3)
    await wrapper.findAll('button').find(b => b.text() === 'Link chapters')!.trigger('click')

    expect(wrapper.emitted('confirm')).toEqual([[{
      source: firstCandidate.source,
      mangaId: firstCandidate.mangaId,
      scanlator: scanlatorBreakdown[0]!.scanlator,
      importance: 3,
    }]])
  })

  it('collapses the untagged (source-name) group to an empty scanlator on confirm', async () => {
    // The breakdown labels a source's untagged chapters under the SOURCE NAME;
    // picking that group must send scanlator "" (all chapters), never the source
    // name — else ingest's filterByScanlator matches zero chapters.
    const untaggedBreakdown: ScanlatorCoverage[] = [
      { scanlator: firstCandidate.sourceName, count: 100, ranges: '1-100' },
      { scanlator: 'Reset Scans', count: 20, ranges: '101-120' },
    ]
    const wrapper = mountDialog({ breakdown: untaggedBreakdown })
    await pickGroupAndCandidate(wrapper)

    // The untagged scan-row's label equals the candidate's sourceName, so target
    // the scan-row by its class to avoid the candidate row's identical aria-label.
    const untaggedRow = wrapper.findAll('.scan-row').find(r => r.text().includes(firstCandidate.sourceName))!
    await untaggedRow.trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'Link chapters')!.trigger('click')

    expect(wrapper.emitted('confirm')).toEqual([[{
      source: firstCandidate.source,
      mangaId: firstCandidate.mangaId,
      scanlator: '',
      importance: 1,
    }]])
  })

  it('picking the "link all" fallback, then confirming, emits confirm with an empty scanlator', async () => {
    const wrapper = mountDialog({ breakdown: null })
    await pickGroupAndCandidate(wrapper)

    await wrapper.findAll('button').find(b => b.text().includes('Coverage unavailable'))!.trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'Link chapters')!.trigger('click')

    expect(wrapper.emitted('confirm')).toEqual([[{
      source: firstCandidate.source,
      mangaId: firstCandidate.mangaId,
      scanlator: '',
      importance: 1,
    }]])
  })

  it('the Link chapters button stays disabled until a scanlator is chosen', async () => {
    const wrapper = mountDialog({ breakdown: scanlatorBreakdown })
    await pickGroupAndCandidate(wrapper)

    const link = wrapper.findAll('button').find(b => b.text() === 'Link chapters')!
    expect(link.attributes('disabled')).toBeDefined()
  })

  it('the Link chapters button stays disabled when the priority is a non-integer', async () => {
    const wrapper = mountDialog({ breakdown: scanlatorBreakdown })
    await pickGroupAndCandidate(wrapper)
    await wrapper.find(`[aria-label="Toggle ${scanlatorBreakdown[0]!.scanlator}"]`).trigger('click')
    await wrapper.find('input[type="number"]').setValue(1.5)

    const link = wrapper.findAll('button').find(b => b.text() === 'Link chapters')!
    expect(link.attributes('disabled')).toBeDefined()

    await link.trigger('click')
    expect(wrapper.emitted('confirm')).toBeUndefined()
  })

  it('surfaces a search/match failure via the error banner', () => {
    const wrapper = mountDialog({ error: 'Suwayomi was unreachable' })
    expect(wrapper.text()).toContain('Suwayomi was unreachable')
  })

  it('disables the Link chapters button while saving (blocks a duplicate submit)', async () => {
    const wrapper = mountDialog({ breakdown: scanlatorBreakdown, saving: true })
    await pickGroupAndCandidate(wrapper)
    await wrapper.find(`[aria-label="Toggle ${scanlatorBreakdown[0]!.scanlator}"]`).trigger('click')

    const link = wrapper.findAll('button').find(b => b.text() === 'Link chapters')!
    expect(link.attributes('disabled')).toBeDefined()
  })

  it('shows a "no matches" note after an empty search result', async () => {
    const wrapper = mountDialog({ groups: [] })
    await wrapper.findAll('button').find(b => b.text() === 'Search')!.trigger('click')

    expect(wrapper.text()).toContain('No matches found')
  })

  it('resets the flow (query, stage, selection) every time it re-opens', async () => {
    const wrapper = mountDialog({ breakdown: scanlatorBreakdown })
    await pickGroupAndCandidate(wrapper)
    expect(wrapper.text()).toContain('Choose the source to link')

    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })

    expect(wrapper.text()).not.toContain('Choose the source to link')
    const input = wrapper.find('input[type="search"]')
    expect((input.element as HTMLInputElement).value).toBe('Solo Leveling')
  })
})
