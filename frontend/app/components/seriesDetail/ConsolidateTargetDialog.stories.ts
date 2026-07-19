import type { Meta, StoryObj } from '@storybook/vue3'
import ConsolidateTargetDialog from './ConsolidateTargetDialog.vue'

/**
 * Stories for the multi-provider consolidation target picker (QCAT-295 Part B).
 * The owner has ticked ≥1 source to fold away; this dialog picks the ONE survivor:
 * an existing provider on the series, or "Match to a new source…" (which hands off
 * to the reused MatchDiskProviderDialog). The page owns the open state and closes
 * it ONLY once the async merge starts, so the error story (dialog still open, error
 * inside) is a real §16 state. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'SeriesDetail/ConsolidateTargetDialog',
  component: ConsolidateTargetDialog,
  parameters: { layout: 'fullscreen' },
} satisfies Meta<typeof ConsolidateTargetDialog>

export default meta
type Story = StoryObj<typeof meta>

const candidates = [
  { id: 'p-real', name: 'QiScans' },
  { id: 'p-other', name: 'Asura Scans' },
]

/** Open: pick an existing survivor or "Match to a new source". */
export const Open: Story = {
  args: { open: true, selectedCount: 3, candidates },
}

/** Only one source selected — singular copy. */
export const SingleSelection: Story = {
  args: { open: true, selectedCount: 1, candidates },
}

/** No other source on the series — only the "Match to a new source" option. */
export const NoCandidates: Story = {
  args: { open: true, selectedCount: 2, candidates: [] },
}

/** In-flight: the confirm button spins ("Merging…") and dismissal is blocked. */
export const Busy: Story = {
  args: { open: true, selectedCount: 3, candidates, busy: true },
}

/** A FAILED merge: the dialog STAYS open with the reason inside it (§16). */
export const WithError: Story = {
  args: { open: true, selectedCount: 3, candidates, error: 'Merge failed' },
}
