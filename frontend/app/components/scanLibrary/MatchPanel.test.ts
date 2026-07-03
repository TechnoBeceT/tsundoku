/**
 * MatchPanel — auto-run coverage for the match→pick→import mutation
 * (behaviour-critical: it changes library state, unlike a play-only story
 * interaction which never executes in `bun run test`). Pins:
 *   1. Picking a group, selecting a candidate, and confirming emits `confirm`
 *      with the EXACT chosen `{source, mangaId, importance}` — importance
 *      defaults to 2 (outranks the disk-origin importance-1 invariant).
 *   2. The top-level `Back` button (at the Groups stage) emits `back`.
 *   3. A match-search failure (`searchError`) renders visibly (§16) instead
 *      of a blank/stuck panel.
 *
 * Non-vacuous: swap the emitted payload's `mangaId`/`source` for the wrong
 * candidate, or drop the `searchError` banner's `v-else-if`, and the matching
 * assertion fails.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import MatchPanel from './MatchPanel.vue'
import { searchResults } from '../../fixtures/import'

describe('MatchPanel', () => {
  it('picking a group then a candidate and confirming emits confirm with the chosen source+importance', async () => {
    const wrapper = mount(MatchPanel, {
      props: {
        title: 'Solo Leveling',
        groups: searchResults,
      },
    })

    // Stage: Groups — pick the first group ("Solo Leveling").
    const groupCards = wrapper.findAll('.group')
    expect(groupCards.length).toBe(searchResults.length)
    await groupCards[0]!.trigger('click')

    // Stage: Candidates — select the MangaDex candidate (first in the group).
    const firstCandidate = searchResults[0]!.candidates[0]!
    const toggle = wrapper.find(`[aria-label="Toggle ${firstCandidate.sourceName}"]`)
    expect(toggle.exists()).toBe(true)
    await toggle.trigger('click')

    // Confirm is disabled until a candidate is selected.
    const confirmButton = wrapper.find('button.btn--primary')
    expect(confirmButton.attributes('disabled')).toBeUndefined()
    await confirmButton.trigger('click')

    const emitted = wrapper.emitted('confirm')
    expect(emitted).toBeTruthy()
    expect(emitted![0]![0]).toEqual({
      source: firstCandidate.source,
      mangaId: firstCandidate.mangaId,
      importance: 2,
    })
  })

  it('the Groups-stage Back button emits back without ever picking a group', async () => {
    const wrapper = mount(MatchPanel, {
      props: {
        title: 'Solo Leveling',
        groups: searchResults,
      },
    })

    await wrapper.find('button.btn--ghost').trigger('click')

    expect(wrapper.emitted('back')).toBeTruthy()
  })

  it('renders a match-search failure instead of a blank panel (§16)', () => {
    const wrapper = mount(MatchPanel, {
      props: {
        title: 'Solo Leveling',
        groups: [],
        searchError: 'Match search failed — the server returned a 500.',
      },
    })

    const alert = wrapper.find('[role="alert"]')
    expect(alert.exists()).toBe(true)
    expect(alert.text()).toContain('Match search failed — the server returned a 500.')
  })

  it('confirm button stays disabled with no candidate selected', async () => {
    const wrapper = mount(MatchPanel, {
      props: {
        title: 'Solo Leveling',
        groups: searchResults,
      },
    })

    await wrapper.findAll('.group')[0]!.trigger('click')

    const confirmButton = wrapper.find('button.btn--primary')
    expect(confirmButton.attributes('disabled')).toBeDefined()
    await confirmButton.trigger('click')
    expect(wrapper.emitted('confirm')).toBeFalsy()
  })
})
