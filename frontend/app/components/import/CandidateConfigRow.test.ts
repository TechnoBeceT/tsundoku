/**
 * CandidateConfigRow — focused render coverage for the `hideInspect`/
 * `hideReorder` opt-in props (GAP-079 item 2): both default `false` so the
 * real Adopt wizard (`screens/Import.vue`) is unaffected, but the two
 * single-select match surfaces (`scanLibrary/MatchPanel`,
 * `seriesDetail/MatchSourceDialog`) set them to suppress the no-op Inspect
 * button and the inert reorder stepper.
 *
 * Non-vacuous: dropping either `v-if` guard in the component makes the
 * corresponding "hidden" assertion fail.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import CandidateConfigRow from './CandidateConfigRow.vue'
import { searchResults } from '../../fixtures/import'

const candidate = searchResults[0]!.candidates[0]!

const baseProps = {
  candidate,
  selected: true,
  rank: 1,
  canUp: false,
  canDown: true,
  inspecting: false,
  inspected: false,
  chapters: [],
}

/** Thin wrapper over the base mount — every test in this file merges its own
 * overrides onto `baseProps` rather than hand-building a fresh props object. */
function mountRow(overrides: Record<string, unknown> = {}) {
  return mount(CandidateConfigRow, { props: { ...baseProps, ...overrides } })
}

describe('CandidateConfigRow', () => {
  it('renders the Inspect button and reorder stepper by default (Import.vue behaviour)', () => {
    const wrapper = mountRow()

    expect(wrapper.find('button.inspect').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'ReorderControl' }).exists()).toBe(true)
  })

  it('hides the Inspect button when hideInspect is set', () => {
    const wrapper = mountRow({ hideInspect: true })

    expect(wrapper.find('button.inspect').exists()).toBe(false)
  })

  it('hides the reorder stepper when hideReorder is set, even while selected', () => {
    const wrapper = mountRow({ hideReorder: true })

    expect(wrapper.findComponent({ name: 'ReorderControl' }).exists()).toBe(false)
  })
})

describe('CandidateConfigRow — coverage states (GAP-140)', () => {
  it('renders a computing state, not "Coverage unavailable", while the walk runs', () => {
    // These are DIFFERENT facts. Showing "unavailable" for work still in flight
    // is what made a slow computation look like a permanent failure, and is why
    // retrying by hand seemed like the only option.
    const w = mountRow({ coverageStatus: 'pending' })

    expect(w.text()).toContain('Computing')
    expect(w.text()).not.toContain('Coverage unavailable')
  })

  it('renders the counts with an as-of date when ready', () => {
    const w = mountRow({
      coverageStatus: 'ready',
      chapterCount: 1301,
      chapterRanges: '1-1301',
      coverageComputedAt: '2026-07-31T09:00:00Z',
    })

    expect(w.text()).toContain('1301 chapters')
    // Without the as-of, a three-day-old snapshot is indistinguishable from a
    // fresh one — the whole reason the result is persisted rather than cached.
    expect(w.text()).toMatch(/as of/i)
  })

  it('renders the reason when the computation failed', () => {
    const w = mountRow({ coverageStatus: 'failed', coverageError: 'upstream timed out' })

    expect(w.text()).toContain('upstream timed out')
  })
})
