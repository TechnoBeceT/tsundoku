import type { Meta, StoryObj } from '@storybook/vue3'
import SuwayomiPane from './SuwayomiPane.vue'
import { flareSolverrConfig, suwayomiConfig } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Suwayomi/engine pane: the proxied SOCKS card + the
 * Tsundoku-owned FlareSolverr card (QCAT-238), each with its own independent
 * §16 SaveFooter. Flip the Storybook theme toolbar to confirm both dark and
 * light.
 */
const meta = {
  title: 'Settings/SuwayomiPane',
  component: SuwayomiPane,
  parameters: { layout: 'padded' },
  args: { config: suwayomiConfig, flareSolverr: flareSolverrConfig },
} satisfies Meta<typeof SuwayomiPane>

export default meta
type Story = StoryObj<typeof meta>

/** Read-only DB, SOCKS off, FlareSolverr on (the seed config). */
export const Default: Story = {
  args: { save: { status: 'idle' }, flareSolverrSave: { status: 'idle' } },
}

/** §16 error — a visible failure message beside the SOCKS Save button. */
export const SaveError: Story = {
  args: { save: { status: 'error', message: 'Save failed — 502 from the engine.' } },
}

/** §16 error — a visible failure message beside the FlareSolverr Save button. */
export const FlareSolverrSaveError: Story = {
  args: { flareSolverrSave: { status: 'error', message: 'Save failed — invalid FlareSolverr URL.' } },
}
