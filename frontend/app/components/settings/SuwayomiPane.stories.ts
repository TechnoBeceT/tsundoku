import type { Meta, StoryObj } from '@storybook/vue3'
import SuwayomiPane from './SuwayomiPane.vue'
import { suwayomiConfig } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the proxied Suwayomi server-config pane (read-only DB + SOCKS +
 * FlareSolverr). Flip the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Settings/SuwayomiPane',
  component: SuwayomiPane,
  parameters: { layout: 'padded' },
  args: { config: suwayomiConfig },
} satisfies Meta<typeof SuwayomiPane>

export default meta
type Story = StoryObj<typeof meta>

/** Read-only DB, SOCKS off, FlareSolverr on (the seed config). */
export const Default: Story = {
  args: { save: { status: 'idle' } },
}

/** §16 error — a visible failure message beside the Save button. */
export const SaveError: Story = {
  args: { save: { status: 'error', message: 'Save failed — 502 from the engine.' } },
}
