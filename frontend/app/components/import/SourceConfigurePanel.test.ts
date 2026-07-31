/**
 * SourceConfigurePanel — pins that the panel actually PASSES the coverage
 * snapshot fields (GAP-140: `coverageStatus`/`coverageComputedAt`/
 * `coverageError`) through to its rendered `<CandidateConfigRow>`s, not just
 * that `CandidateConfigRow` itself can render them in isolation.
 *
 * This is the parent-mount test the row-level suite (`CandidateConfigRow.
 * test.ts`) cannot provide: that file hand-builds props straight onto the
 * row, which proves the row CAN render the three states but says nothing
 * about whether any real screen ever supplies them. `SourceConfigurePanel` is
 * the actual, only parent that renders `<CandidateConfigRow>` with computed
 * coverage data (via `useSourceConfigure`'s `DisplayRow[]`) — mounting it and
 * asserting on its rendered text proves the wiring across the component
 * boundary, not just the leaf.
 */
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SourceConfigurePanel from './SourceConfigurePanel.vue'
import { searchResults } from '../../fixtures/import'
import type { DisplayRow } from '~/composables/useSourceConfigure'

const candidate = searchResults[0]!.candidates[0]!

function row(overrides: Partial<DisplayRow> = {}): DisplayRow {
  return {
    key: 'src-1:1',
    candidate,
    scanlator: '',
    scanlatorParam: '',
    chapterCount: undefined,
    chapterRanges: '',
    coverageUnavailable: false,
    isSplit: false,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: false,
    ...overrides,
  }
}

describe('SourceConfigurePanel — coverage snapshot pass-through (GAP-140)', () => {
  it('renders the computing state for a row whose snapshot is pending', () => {
    const wrapper = mount(SourceConfigurePanel, {
      props: { rows: [row({ coverageStatus: 'pending' })] },
    })

    expect(wrapper.text()).toContain('Computing')
    expect(wrapper.text()).not.toContain('Coverage unavailable')
  })

  it('renders the counts and as-of date for a row whose snapshot is ready', () => {
    const wrapper = mount(SourceConfigurePanel, {
      props: {
        rows: [row({
          coverageStatus: 'ready',
          chapterCount: 1301,
          chapterRanges: '1-1301',
          coverageComputedAt: '2026-07-31T09:00:00Z',
        })],
      },
    })

    expect(wrapper.text()).toContain('1301 chapters')
    expect(wrapper.text()).toMatch(/as of/i)
  })

  it('renders the reason for a row whose snapshot failed', () => {
    const wrapper = mount(SourceConfigurePanel, {
      props: {
        rows: [row({ coverageStatus: 'failed', coverageError: 'upstream timed out' })],
      },
    })

    expect(wrapper.text()).toContain('upstream timed out')
  })
})
