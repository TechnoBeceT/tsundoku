/**
 * Import — Stage 2 (Configure) per-scanlator auto-split (GAP: scanlator-aware
 * providers, Task 6). Pins:
 *   1. Entering Stage 2 (picking a group) emits `loadBreakdowns` with that
 *      group's candidates — the parent (`useImport.loadBreakdowns`) fetches
 *      the per-source breakdown from there.
 *   2. A candidate whose `breakdowns` entry resolves with 2+ scanlators
 *      renders one row PER scanlator, each with its own inline "N chapters ·
 *      ranges" coverage; a candidate with exactly one scanlator (even once
 *      loaded) or with no/failed breakdown stays a SINGLE row.
 *   3. A single-scanlator breakdown whose one group is named after the source
 *      itself (the backend's "untagged" convention) shows no scanlator
 *      subtitle and adopts as scanlator "" (all chapters) — never filtered to
 *      its own name.
 *   4. `adopt()` sends one `AdoptProvider` per selected row, each carrying
 *      that row's own `scanlator` (raw name for a split row, "" for an
 *      unsplit/untagged/unavailable row), ranked by the SAME global order as
 *      today (rank spans every source/scanlator row, not per-source).
 *
 * `breakdowns` is supplied fully pre-populated via props (this component never
 * fetches) — mirrors how `useImport`'s `breakdowns` ref would look once its
 * `loadBreakdowns()` calls have resolved.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import Import from './Import.vue'
import type { ScanlatorCoverage } from './import.types'
import { categories, searchResults, sources } from '../../fixtures/import'

const group = searchResults[0]! // "Solo Leveling" — 3 candidates: MangaDex, Asura Scans, Manganato
const mangaDex = group.candidates[0]! // source '2499283573021220255', mangaId 1001
const asura = group.candidates[1]! // source '1024627298672457456', mangaId 1002, sourceName 'Asura Scans'
const manganato = group.candidates[2]! // source '3437691801785968169', mangaId 1003

const breakdownKey = (source: string, mangaId: number): string => `${source}:${mangaId}`

/** MangaDex: a genuine 2-scanlator split. Asura Scans: a single UNTAGGED group (named after the source itself). Manganato: no entry (breakdown never loaded/failed). */
const breakdowns: Record<string, ScanlatorCoverage[] | null> = {
  [breakdownKey(mangaDex.source, mangaDex.mangaId)]: [
    { scanlator: 'ZScans', count: 90, ranges: '1-90' },
    { scanlator: 'HiveToons', count: 11, ranges: '92-101' },
  ],
  [breakdownKey(asura.source, asura.mangaId)]: [
    { scanlator: asura.sourceName, count: 50, ranges: '1-50' },
  ],
}

function mountAtStage2(breakdownsProp: Record<string, ScanlatorCoverage[] | null> = breakdowns) {
  const wrapper = mount(Import, {
    props: {
      sources,
      searchResults: [group],
      searched: true,
      categories,
      breakdowns: breakdownsProp,
    },
  })
  return wrapper
}

async function pickGroup(wrapper: ReturnType<typeof mountAtStage2>) {
  await wrapper.find('.group').trigger('click')
}

function findButtonByText(wrapper: ReturnType<typeof mountAtStage2>, text: string) {
  const btn = wrapper.findAll('button').find(b => b.text().includes(text))
  if (!btn) throw new Error(`no button found with text "${text}"`)
  return btn
}

describe('Import — Stage 2 auto-split', () => {
  it('picking a group emits loadBreakdowns with that group\'s candidates', async () => {
    const wrapper = mountAtStage2({})
    await pickGroup(wrapper)

    const emitted = wrapper.emitted('loadBreakdowns')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual(group.candidates)
  })

  it('splits a 2-scanlator source into 2 rows with the correct count + ranges, leaves a 1-scanlator/unloaded source as 1 row', async () => {
    const wrapper = mountAtStage2()
    await pickGroup(wrapper)

    // 2 (MangaDex split) + 1 (Asura Scans, single) + 1 (Manganato, no breakdown) = 4 rows.
    expect(wrapper.findAll('.cand').length).toBe(4)

    const text = wrapper.text()
    // MangaDex's two scanlator rows: subtitle + inline coverage each.
    expect(text).toContain('ZScans')
    expect(text).toContain('90 chapters')
    expect(text).toContain('1-90')
    expect(text).toContain('HiveToons')
    expect(text).toContain('11 chapters')
    expect(text).toContain('92-101')

    // Asura Scans is a single row (1 scanlator) but still shows inline coverage.
    expect(text).toContain('50 chapters')
    expect(text).toContain('1-50')

    // The untagged group (scanlator === sourceName) never renders as a subtitle.
    expect(wrapper.findAll('.cand__scanlator').map(s => s.text())).toEqual(['ZScans', 'HiveToons'])

    // Manganato (no breakdown entry) has no coverage line at all — only the
    // 3 loaded rows (2 MangaDex + 1 Asura) show one.
    expect(wrapper.findAll('.cand__coverage').length).toBe(3)
  })

  it('shows "Coverage unavailable" for a source whose breakdown fetch failed (non-fatal, still 1 row)', async () => {
    const wrapper = mountAtStage2({
      ...breakdowns,
      [breakdownKey(manganato.source, manganato.mangaId)]: null,
    })
    await pickGroup(wrapper)

    expect(wrapper.findAll('.cand').length).toBe(4)
    expect(wrapper.text()).toContain('Coverage unavailable')
  })
})

describe('Import — adopt() with per-scanlator rows', () => {
  it('sends one AdoptProvider per selected row, ranked globally, with the right scanlator (named for a split row, "" for the untagged/unsplit rows)', async () => {
    const wrapper = mountAtStage2()
    await pickGroup(wrapper)

    await findButtonByText(wrapper, 'Review').trigger('click')
    await findButtonByText(wrapper, 'Adopt series').trigger('click')

    const emitted = wrapper.emitted('adopt')
    expect(emitted).toBeTruthy()
    const request = emitted![0]![0] as { providers: unknown[] }
    expect(request.providers).toEqual([
      { source: mangaDex.source, mangaId: mangaDex.mangaId, importance: 40, scanlator: 'ZScans' },
      { source: mangaDex.source, mangaId: mangaDex.mangaId, importance: 30, scanlator: 'HiveToons' },
      { source: asura.source, mangaId: asura.mangaId, importance: 20, scanlator: '' },
      { source: manganato.source, mangaId: manganato.mangaId, importance: 10, scanlator: '' },
    ])
  })

  it('collapses the source-name (untagged) group to scanlator "" even INSIDE a 2+-group split, sending "" for it and the raw name for a genuinely-named sibling group', async () => {
    // A MIX of tagged + untagged chapters: the breakdown has 2 groups — one
    // named after the source itself (the SourceBreakdown untagged bucket) and
    // one a real scanlation group. The untagged row MUST adopt as scanlator ""
    // (matches the backend's untagged Chapter.Scanlator=="") — sending the
    // source name would filter to zero chapters (a silently-empty provider).
    const wrapper = mountAtStage2({
      [breakdownKey(mangaDex.source, mangaDex.mangaId)]: [
        { scanlator: mangaDex.sourceName, count: 40, ranges: '1-40' }, // untagged bucket
        { scanlator: 'ZScans', count: 60, ranges: '41-100' }, // genuinely-named group
      ],
      // Asura + Manganato collapse to a single row each (out of scope here) —
      // omit their breakdowns so they stay unsplit and don't clutter the assert.
    })
    await pickGroup(wrapper)

    await findButtonByText(wrapper, 'Review').trigger('click')
    await findButtonByText(wrapper, 'Adopt series').trigger('click')

    const emitted = wrapper.emitted('adopt')
    expect(emitted).toBeTruthy()
    const request = emitted![0]![0] as { providers: { source: string, scanlator: string }[] }
    const mangaDexRows = request.providers.filter(p => p.source === mangaDex.source)
    expect(mangaDexRows.map(p => p.scanlator)).toEqual(['', 'ZScans'])
  })
})
