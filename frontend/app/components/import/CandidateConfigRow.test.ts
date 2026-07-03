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

describe('CandidateConfigRow', () => {
  it('renders the Inspect button and reorder stepper by default (Import.vue behaviour)', () => {
    const wrapper = mount(CandidateConfigRow, { props: baseProps })

    expect(wrapper.find('button.inspect').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'ReorderControl' }).exists()).toBe(true)
  })

  it('hides the Inspect button when hideInspect is set', () => {
    const wrapper = mount(CandidateConfigRow, { props: { ...baseProps, hideInspect: true } })

    expect(wrapper.find('button.inspect').exists()).toBe(false)
  })

  it('hides the reorder stepper when hideReorder is set, even while selected', () => {
    const wrapper = mount(CandidateConfigRow, { props: { ...baseProps, hideReorder: true } })

    expect(wrapper.findComponent({ name: 'ReorderControl' }).exists()).toBe(false)
  })
})
