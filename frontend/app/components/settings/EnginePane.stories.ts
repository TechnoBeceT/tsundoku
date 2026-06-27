import type { Meta, StoryObj } from '@storybook/vue3'
import EnginePane from './EnginePane.vue'
import { engineInfo, upgradeStepsInProgress } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the read-only Engine pane. Flip the Storybook theme toolbar to
 * confirm both dark and light.
 */
const meta = {
  title: 'Settings/EnginePane',
  component: EnginePane,
  parameters: { layout: 'padded' },
  args: { engine: engineInfo },
} satisfies Meta<typeof EnginePane>

export default meta
type Story = StoryObj<typeof meta>

/** Embedded engine, running, with a newer pinned version available to upgrade. */
export const UpgradeAvailable: Story = {}

/** Mid-upgrade — the vertical progress stepper (Swap JAR in flight). */
export const Upgrading: Story = {
  args: { upgradeSteps: upgradeStepsInProgress, upgrading: true },
}

/** Up to date — no upgrade affordance. */
export const UpToDate: Story = {
  args: { engine: { ...engineInfo, upgradeAvailable: false } },
}

/** External mode — Tsundoku doesn't manage the lifecycle; only the URL shows. */
export const External: Story = {
  args: { engine: { ...engineInfo, mode: 'external' } },
}
