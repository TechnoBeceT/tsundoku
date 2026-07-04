import type { Meta, StoryObj } from '@storybook/vue3'
import CandidateConfigRow from './CandidateConfigRow.vue'
import { inspectChapters, searchResults } from '../../fixtures/import'

/**
 * Stories for one Stage-2 configure row. `toggle`, `inspect`, and `move` are
 * logged in the Actions panel. The variants cover the row states: selected (with
 * the rank stepper), unselected (no stepper), inspect loading (spinner), and
 * inspect resolved (<ChapterInspectList>). Flip the Storybook theme toolbar to
 * confirm both themes.
 */
const meta = {
  title: 'Import/CandidateConfigRow',
  component: CandidateConfigRow,
  parameters: {
    layout: 'padded',
    actions: { handles: ['toggle', 'inspect', 'move'] },
  },
  decorators: [() => ({ template: '<div style="max-width:780px"><story /></div>' })],
} satisfies Meta<typeof CandidateConfigRow>

export default meta
type Story = StoryObj<typeof meta>

const candidate = searchResults[0]!.candidates[0]!
const noCover = searchResults[0]!.candidates[2]!

/** Selected, rank 1 (preferred) — the rank stepper is shown, up disabled. */
export const Selected: Story = {
  args: {
    candidate,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: true,
    inspecting: false,
    inspected: false,
    chapters: [],
  },
}

/** Selected, mid-rank — both arrows enabled. */
export const SelectedMidRank: Story = {
  args: {
    candidate,
    selected: true,
    rank: 2,
    canUp: true,
    canDown: true,
    inspecting: false,
    inspected: false,
    chapters: [],
  },
}

/** Unselected (placeholder cover) — no rank stepper. */
export const Unselected: Story = {
  args: {
    candidate: noCover,
    selected: false,
    rank: 0,
    canUp: false,
    canDown: false,
    inspecting: false,
    inspected: false,
    chapters: [],
  },
}

/** Inspect in flight — the loading spinner (§16). */
export const Inspecting: Story = {
  args: {
    candidate,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: true,
    inspecting: true,
    inspected: false,
    chapters: [],
  },
}

/** Inspect resolved — the chapter preview list (§16). */
export const Inspected: Story = {
  args: {
    candidate,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: true,
    inspecting: false,
    inspected: true,
    chapters: inspectChapters,
  },
}

/**
 * Selected + `hideInspect`/`hideReorder` both true — the shape rendered by
 * the two single-select match surfaces (`scanLibrary/MatchPanel`,
 * `seriesDetail/MatchSourceDialog`): neither the no-op Inspect button nor the
 * inert reorder stepper appears, even though `selected` would otherwise show
 * the stepper.
 */
export const HiddenInspectAndReorder: Story = {
  args: {
    candidate,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: true,
    inspecting: false,
    inspected: false,
    chapters: [],
    hideInspect: true,
    hideReorder: true,
  },
}

/**
 * The Adopt wizard's auto-split rows (Import.vue Stage 2 — see `configRows`).
 * A source with only ONE scanlator group still shows its inline coverage
 * (count + ranges); the scanlator subtitle only appears when the group is
 * genuinely named (not the source's own "untagged" name).
 */
export const SingleScanlatorCoverage: Story = {
  args: {
    candidate,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: true,
    inspecting: false,
    inspected: false,
    chapters: [],
    scanlator: 'ZScans',
    chapterCount: 42,
    chapterRanges: '1-42',
  },
}

/**
 * One row of a MULTI-scanlator split (2+ groups from the same source) — the
 * Inspect button is hidden (`hideInspect`) since coverage is already inline.
 */
export const MultiScanlatorRow: Story = {
  args: {
    candidate,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: true,
    inspecting: false,
    inspected: false,
    chapters: [],
    hideInspect: true,
    scanlator: 'HiveToons',
    chapterCount: 58,
    chapterRanges: '43-90, 92-101',
  },
}

/** A row with gaps in its coverage — the ranges string surfaces the hole (e.g. a missing chapter 91). */
export const CoverageWithGaps: Story = {
  args: {
    candidate,
    selected: true,
    rank: 2,
    canUp: true,
    canDown: false,
    inspecting: false,
    inspected: false,
    chapters: [],
    hideInspect: true,
    scanlator: 'Comix',
    chapterCount: 99,
    chapterRanges: '1-90, 92-101',
  },
}

/**
 * The breakdown fetch failed for this source (non-fatal — see
 * `useImport.loadBreakdowns`): a single unsplit row with a "Coverage
 * unavailable" note instead of a count, and the Inspect button still shown
 * (there's no inline info to fall back on).
 */
export const CoverageUnavailable: Story = {
  args: {
    candidate,
    selected: true,
    rank: 1,
    canUp: false,
    canDown: true,
    inspecting: false,
    inspected: false,
    chapters: [],
    coverageUnavailable: true,
  },
}
