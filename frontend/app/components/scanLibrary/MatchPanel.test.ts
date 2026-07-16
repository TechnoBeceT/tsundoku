/**
 * MatchPanel — Slice P rebuild onto `useSourceConfigure` (multi-select tray +
 * Configure powers), mirroring `MatchSourceDialog.test.ts`'s coverage shape.
 * Auto-run (behaviour-critical: it changes library state, unlike a play-only
 * story interaction which never executes in `bun run test`). Pins:
 *   1. Picking a group advances to Configure with every candidate selected by
 *      default; confirming emits the EXACT ordered `ProviderRef[]`
 *      (best-first, no importance — Slice P widened this from a single
 *      `{source, mangaId, importance}`).
 *   2. `loadBreakdowns` is emitted with the picked group's candidates as soon
 *      as Configure is entered (Configure-stage coverage auto-split entry).
 *   3. Toggling a candidate off drops it from the emitted set; reordering
 *      changes the emitted order.
 *   4. The Groups-stage `Back` button emits `back`; the Confirm button stays
 *      disabled with nothing selected.
 *   5. A match-search failure (`searchError`) renders visibly (§16) instead
 *      of a blank/stuck panel.
 *   6. Tray-enabled: gathering two groups via "+ Add" then "Configure N
 *      sources →" enters Configure with every gathered candidate (this
 *      surface is intentionally MULTI-select, unlike its single-select
 *      sibling `MatchDiskProviderDialog`, which is untouched by this slice
 *      and renders no tray toggle at all).
 *
 * Non-vacuous: swap the emitted payload's `mangaId`/`source`/order for the
 * wrong candidate, or drop the `searchError` banner's `v-else-if`, and the
 * matching assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import MatchPanel from './MatchPanel.vue'
import { searchResults } from '../../fixtures/import'

// Every candidate's breakdown resolved (as a failed/unavailable lookup, `null`)
// by default — so `breakdownsResolving` is false and the Configure-stage
// Attach button is enabled out of the box, mirroring a settled real fetch.
const resolvedBreakdowns = Object.fromEntries(
  [...searchResults[0]!.candidates, ...searchResults[1]!.candidates].map(c => [`${c.source}:${c.mangaId}`, null]),
)

function mountPanel(props: Record<string, unknown> = {}) {
  return mount(MatchPanel, {
    props: { title: 'Solo Leveling', groups: searchResults, breakdowns: resolvedBreakdowns, ...props },
  })
}

/** Finds one `SearchGroupCard` by its title text (mirrors Import.test.ts's helper). */
function findGroupCard(wrapper: VueWrapper, title: string) {
  return wrapper.findAll('.group').find(g => g.text().includes(title))!
}

describe('MatchPanel', () => {
  it('advances to the configure stage and lists every candidate, all selected by default, after picking a group', async () => {
    const wrapper = mountPanel()
    await wrapper.find('.group').trigger('click')

    for (const candidate of searchResults[0]!.candidates) {
      expect(wrapper.text()).toContain(candidate.sourceName)
    }
    // Every candidate starts selected (useSourceConfigure.enterConfigure) —
    // the Attach button is enabled with no toggling required.
    const attach = wrapper.findAll('button').find(b => b.text().startsWith('Attach'))!
    expect(attach.attributes('disabled')).toBeUndefined()
    expect(attach.text()).toBe(`Attach ${searchResults[0]!.candidates.length} sources`)
  })

  it('emits loadBreakdowns with the picked group\'s candidates when advancing to Configure', async () => {
    const wrapper = mountPanel()
    await wrapper.find('.group').trigger('click')

    const emitted = wrapper.emitted('loadBreakdowns')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual(searchResults[0]!.candidates)
  })

  it('confirming with the default all-selected set emits an ordered ProviderRef[] (best-first, source order)', async () => {
    const wrapper = mountPanel()
    await wrapper.find('.group').trigger('click')
    await wrapper.findAll('button').find(b => b.text().startsWith('Attach'))!.trigger('click')

    expect(wrapper.emitted('confirm')).toEqual([[
      searchResults[0]!.candidates.map(c => ({ source: c.source, mangaId: c.mangaId, scanlator: '', url: c.url })),
    ]])
  })

  it('toggling a candidate off then confirming emits confirm without it', async () => {
    const wrapper = mountPanel()
    await wrapper.find('.group').trigger('click')

    const target = searchResults[0]!.candidates[1]!
    await wrapper.find(`[aria-label="Toggle ${target.sourceName}"]`).trigger('click')
    await wrapper.findAll('button').find(b => b.text().startsWith('Attach'))!.trigger('click')

    const providers = wrapper.emitted('confirm')![0]![0] as { source: string, mangaId: number }[]
    expect(providers.some(p => p.source === target.source && p.mangaId === target.mangaId)).toBe(false)
    expect(providers.length).toBe(searchResults[0]!.candidates.length - 1)
  })

  it('reordering a candidate with the rank arrows changes the emitted order (best-first)', async () => {
    const wrapper = mountPanel()
    await wrapper.find('.group').trigger('click')

    // Move the second candidate up one rank, ahead of the first.
    const moveUpButtons = wrapper.findAll('[aria-label="Move up"]')
    await moveUpButtons[1]!.trigger('click')
    await wrapper.findAll('button').find(b => b.text().startsWith('Attach'))!.trigger('click')

    const providers = wrapper.emitted('confirm')![0]![0] as { source: string, mangaId: number }[]
    expect(providers[0]!.source).toBe(searchResults[0]!.candidates[1]!.source)
    expect(providers[1]!.source).toBe(searchResults[0]!.candidates[0]!.source)
  })

  it('the Groups-stage Back button emits back without ever picking a group', async () => {
    const wrapper = mountPanel()

    await wrapper.find('button.btn--ghost').trigger('click')

    expect(wrapper.emitted('back')).toBeTruthy()
  })

  it('the Attach button stays disabled until at least one candidate is selected', async () => {
    const wrapper = mountPanel()
    await wrapper.find('.group').trigger('click')

    for (const c of searchResults[0]!.candidates) {
      await wrapper.find(`[aria-label="Toggle ${c.sourceName}"]`).trigger('click')
    }

    const attach = wrapper.findAll('button').find(b => b.text().startsWith('Attach'))!
    expect(attach.attributes('disabled')).toBeDefined()
    await attach.trigger('click')
    expect(wrapper.emitted('confirm')).toBeFalsy()
  })

  it('renders a match-search failure instead of a blank panel (§16)', () => {
    const wrapper = mountPanel({
      groups: [],
      searchError: 'Match search failed — the server returned a 500.',
    })

    const alert = wrapper.find('[role="alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('Match search failed — the server returned a 500.')
  })

  it('disables the Attach button while the import mutation is busy (blocks a duplicate submit)', async () => {
    const wrapper = mountPanel({ busy: true })
    await wrapper.find('.group').trigger('click')

    const attach = wrapper.findAll('button').find(b => b.text().startsWith('Attach'))!
    expect(attach.attributes('disabled')).toBeDefined()
  })

  it('a fresh set of groups (a new match search) resets to the Groups stage', async () => {
    const wrapper = mountPanel()
    await wrapper.find('.group').trigger('click')
    expect(wrapper.emitted('loadBreakdowns')).toBeTruthy()

    // A new search's results replace `groups` — must not leave the stale
    // Configure selection showing.
    await wrapper.setProps({ groups: [searchResults[1]!] })

    const attach = wrapper.findAll('button').find(b => b.text().startsWith('Attach'))
    expect(attach).toBeUndefined()
    expect(wrapper.text()).toContain(searchResults[1]!.title)
  })

  // ---- Cross-search gather tray (Slice P — this surface is now multi-select) ----

  it('gathers a group into the tray via "+ Add", then "Configure N sources →" enters Configure with every tray candidate', async () => {
    const wrapper = mountPanel()
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

  it('renders the "+ Add" tray toggle on every group card (tray-enabled is ON for this surface)', () => {
    const wrapper = mountPanel()
    const toggles = wrapper.findAll('.group__toggle')
    expect(toggles.length).toBe(searchResults.length)
    for (const t of toggles) expect(t.text()).toBe('+ Add')
  })
})
