/**
 * MatchSourceDialog — Slice P rebuild onto `useSourceConfigure` (multi-select
 * tray + Configure powers). Drives the search → configure flow and asserts
 * the emitted payloads (now an ordered `ProviderRef[]`, best-first, instead
 * of the old single `{source, mangaId, importance}`), plus the §16
 * loading/error surfaces and the cross-search adopt tray.
 *
 * The real Dialog teleports its body through reka-ui's portal (which does not
 * render in happy-dom), so it is stubbed to render its default + actions
 * slots inline (mirrors ExtensionPreferencesDialog.test.ts). That keeps the
 * assertions on the dialog's OWN behaviour — the two-stage flow, the emitted
 * search/loadBreakdowns/confirm payloads, the saving/error surfaces, and the
 * tray — not on reka.
 */
import { describe, it, expect } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import MatchSourceDialog from './MatchSourceDialog.vue'
import { searchResults } from '../../fixtures/import'

const DialogStub = { template: '<div class="dialog-stub"><slot /><slot name="actions" /></div>' }

// Every candidate's breakdown resolved (as a failed/unavailable lookup, `null`)
// by default — so `breakdownsResolving` is false and the Configure-stage
// Attach button is enabled out of the box, mirroring a settled real fetch.
// Tests that care about the split behaviour override individual keys.
const resolvedBreakdowns = Object.fromEntries(
  searchResults[0]!.candidates.map(c => [`${c.source}:${c.mangaId}`, null]),
)

function mountDialog(props: Record<string, unknown> = {}) {
  return mount(MatchSourceDialog, {
    props: { open: true, seriesTitle: 'Solo Leveling', groups: searchResults, breakdowns: resolvedBreakdowns, ...props },
    global: { stubs: { Dialog: DialogStub } },
  })
}

/** Finds one `SearchGroupCard` by its title text (mirrors Import.test.ts's helper). */
function findGroupCard(wrapper: VueWrapper, title: string) {
  return wrapper.findAll('.group').find(g => g.text().includes(title))!
}

describe('MatchSourceDialog', () => {
  it('prefills the search box with the series title', () => {
    const wrapper = mountDialog()
    const input = wrapper.find('input[type="search"]')
    expect((input.element as HTMLInputElement).value).toBe('Solo Leveling')
  })

  it('emits search with the trimmed query (and empty sources) on Search click', async () => {
    const wrapper = mountDialog()
    await wrapper.find('input[type="search"]').setValue('  naruto  ')
    await wrapper.findAll('button').find(b => b.text() === 'Search')!.trigger('click')

    expect(wrapper.emitted('search')).toEqual([[{ q: 'naruto', sources: [] }]])
  })

  it('renders the source-filter chips when sources are supplied and includes the selected IDs in the search payload', async () => {
    const sources = [
      { id: 'src-a', name: 'MangaDex', lang: 'en' },
      { id: 'src-b', name: 'Asura Scans', lang: 'en' },
    ]
    const wrapper = mountDialog({ sources })

    // Chip row rendered from the sources prop.
    const chips = wrapper.findAll('button.imp-chip')
    expect(chips.map(c => c.text())).toEqual(['MangaDex', 'Asura Scans'])

    // Select the first source, then search — the payload carries its ID.
    await chips[0]!.trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'Search')!.trigger('click')

    expect(wrapper.emitted('search')).toEqual([[{ q: 'Solo Leveling', sources: ['src-a'] }]])
  })

  it('does not render the chip row when no sources are supplied', () => {
    const wrapper = mountDialog()
    expect(wrapper.find('button.imp-chip').exists()).toBe(false)
  })

  it('resets the source filter selection on re-open', async () => {
    const sources = [{ id: 'src-a', name: 'MangaDex', lang: 'en' }]
    const wrapper = mountDialog({ sources })

    await wrapper.find('button.imp-chip').trigger('click')
    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })
    await wrapper.findAll('button').find(b => b.text() === 'Search')!.trigger('click')

    // The last search emit carries an empty sources list — the selection was cleared.
    const emitted = wrapper.emitted('search')!
    expect(emitted[emitted.length - 1]).toEqual([{ q: 'Solo Leveling', sources: [] }])
  })

  it('advances to the configure stage and lists every candidate, all selected by default, after picking a group', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    for (const candidate of searchResults[0]!.candidates) {
      expect(wrapper.text()).toContain(candidate.sourceName)
    }
    // Every candidate starts selected (useSourceConfigure.enterConfigure) —
    // the Attach button is enabled with no toggling required.
    const attach = wrapper.findAll('button').find(b => b.text() === 'Attach sources')!
    expect(attach.attributes('disabled')).toBeUndefined()
  })

  it('emits loadBreakdowns with the picked group\'s candidates when advancing to Configure', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    const emitted = wrapper.emitted('loadBreakdowns')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual(searchResults[0]!.candidates)
  })

  it('confirming with the default all-selected set emits an ordered ProviderRef[] (best-first, source order)', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'Attach sources')!.trigger('click')

    expect(wrapper.emitted('confirm')).toEqual([[
      searchResults[0]!.candidates.map(c => ({ source: c.source, mangaId: c.mangaId, scanlator: '' })),
    ]])
  })

  it('collapses the untagged (source-name) breakdown group to an empty scanlator on confirm', async () => {
    // The breakdown labels a source's untagged chapters under the SOURCE NAME;
    // via useSourceConfigure's shared collapse the emitted payload must be ""
    // (all chapters) — NOT the source name (which would match zero chapters).
    const first = searchResults[0]!.candidates[0]! // MangaDex / 1001
    const breakdowns = {
      ...resolvedBreakdowns,
      [`${first.source}:${first.mangaId}`]: [
        { scanlator: first.sourceName, count: 100, ranges: '1-100' },
      ],
    }
    const wrapper = mountDialog({ breakdowns })
    await wrapper.find('.group').trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'Attach sources')!.trigger('click')

    const providers = wrapper.emitted('confirm')![0]![0] as { source: string, mangaId: number, scanlator: string }[]
    const mangadex = providers.find(p => p.source === first.source && p.mangaId === first.mangaId)!
    expect(mangadex.scanlator).toBe('')
  })

  it('toggling a candidate off then confirming emits confirm without it', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    const target = searchResults[0]!.candidates[1]!
    await wrapper.find(`[aria-label="Toggle ${target.sourceName}"]`).trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'Attach sources')!.trigger('click')

    const providers = wrapper.emitted('confirm')![0]![0] as { source: string, mangaId: number }[]
    expect(providers.some(p => p.source === target.source && p.mangaId === target.mangaId)).toBe(false)
    expect(providers.length).toBe(searchResults[0]!.candidates.length - 1)
  })

  it('reordering a candidate with the rank arrows changes the emitted order (best-first)', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    // Move the second candidate up one rank, ahead of the first.
    const moveUpButtons = wrapper.findAll('[aria-label="Move up"]')
    await moveUpButtons[1]!.trigger('click')
    await wrapper.findAll('button').find(b => b.text() === 'Attach sources')!.trigger('click')

    const providers = wrapper.emitted('confirm')![0]![0] as { source: string, mangaId: number }[]
    expect(providers[0]!.source).toBe(searchResults[0]!.candidates[1]!.source)
    expect(providers[1]!.source).toBe(searchResults[0]!.candidates[0]!.source)
  })

  it('the Attach button stays disabled until at least one candidate is selected', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')

    // Deselect every candidate.
    for (const c of searchResults[0]!.candidates) {
      await wrapper.find(`[aria-label="Toggle ${c.sourceName}"]`).trigger('click')
    }

    const attach = wrapper.findAll('button').find(b => b.text() === 'Attach sources')!
    expect(attach.attributes('disabled')).toBeDefined()
  })

  it('surfaces a search/attach failure via the error banner', () => {
    const wrapper = mountDialog({ error: 'Suwayomi was unreachable' })
    expect(wrapper.text()).toContain('Suwayomi was unreachable')
  })

  it('disables the Attach button while saving (blocks a duplicate submit)', async () => {
    const wrapper = mountDialog({ saving: true })
    await wrapper.find('.group').trigger('click')

    const attach = wrapper.findAll('button').find(b => b.text() === 'Attach sources')!
    expect(attach.attributes('disabled')).toBeDefined()
  })

  it('shows a "no matches" note after an empty search result', async () => {
    const wrapper = mountDialog({ groups: [] })
    await wrapper.findAll('button').find(b => b.text() === 'Search')!.trigger('click')

    expect(wrapper.text()).toContain('No matches found')
  })

  it('resets the flow (query, stage, tray, pick) every time it re-opens', async () => {
    const wrapper = mountDialog()
    await wrapper.find('.group').trigger('click')
    expect(wrapper.text()).toContain('Adding sources to')

    // Close then re-open — the stale "configure" stage must not survive.
    await wrapper.setProps({ open: false })
    await wrapper.setProps({ open: true })

    expect(wrapper.text()).not.toContain('Adding sources to')
    const input = wrapper.find('input[type="search"]')
    expect((input.element as HTMLInputElement).value).toBe('Solo Leveling')
  })

  // ---- Cross-search adopt tray (this surface is intentionally multi-select) ----

  it('gathers a group into the tray via "+ Add", then "Configure N sources →" enters Configure with every tray candidate', async () => {
    const wrapper = mountDialog()
    const [g1, g2] = searchResults

    await findGroupCard(wrapper, g1!.title).find('.group__toggle').trigger('click')
    expect(wrapper.text()).toContain(`${g1!.candidates.length} sources`)

    // While the tray is non-empty, a card body click no longer picks straight
    // to Configure — the second group's "+ Add" toggle still works.
    await findGroupCard(wrapper, g2!.title).find('.group__toggle').trigger('click')
    expect(wrapper.text()).toContain(`${g1!.candidates.length + g2!.candidates.length} sources`)

    await wrapper.findAll('button').find(b => b.text().startsWith('Configure'))!.trigger('click')

    for (const candidate of [...g1!.candidates, ...g2!.candidates]) {
      expect(wrapper.text()).toContain(candidate.sourceName)
    }
  })

  // ---- Tray-leak guard: this surface intentionally OPTS IN to tray-enabled ----
  // (contrast with the single-select `MatchDiskProviderDialog`/`MatchPanel`,
  // which pass no `tray-enabled` prop to `SearchGroupCard` and therefore never
  // render this toggle — verified by inspection, not exercised here).

  it('renders the "+ Add" tray toggle on every group card (tray-enabled is ON for this surface)', () => {
    const wrapper = mountDialog()
    const toggles = wrapper.findAll('.group__toggle')
    expect(toggles.length).toBe(searchResults.length)
    for (const t of toggles) expect(t.text()).toBe('+ Add')
  })
})
