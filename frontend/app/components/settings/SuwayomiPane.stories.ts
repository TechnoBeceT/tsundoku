import type { Meta, StoryObj } from '@storybook/vue3'
import SuwayomiPane from './SuwayomiPane.vue'
import { flareSolverrConfig, impersonateConfig } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the global Access & bypass controls — two Tsundoku-owned cards, each with its
 * own §16 SaveFooter: the FlareSolverr card (QCAT-238) and the impersonate-gateway
 * card (GAP-111). The proxied Suwayomi SOCKS card was RETIRED with the P2
 * Suwayomi-removal backend cutover. Per-source proxy membership now lives only
 * in Source exceptions. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'Settings/SuwayomiPane',
  component: SuwayomiPane,
  parameters: { layout: 'padded' },
  args: { flareSolverr: flareSolverrConfig, impersonate: impersonateConfig },
} satisfies Meta<typeof SuwayomiPane>

export default meta
type Story = StoryObj<typeof meta>

/** Both cards on (the seed config). */
export const Default: Story = {
  args: { flareSolverrSave: { status: 'idle' }, impersonateSave: { status: 'idle' } },
}

/** §16 error — a visible failure message beside the FlareSolverr Save button. */
export const FlareSolverrSaveError: Story = {
  args: { flareSolverrSave: { status: 'error', message: 'Save failed — invalid FlareSolverr URL.' } },
}

/** §16 error — a visible failure message beside the image-proxy Save button. */
export const ImpersonateSaveError: Story = {
  args: { impersonateSave: { status: 'error', message: 'Save failed — invalid gateway URL.' } },
}
