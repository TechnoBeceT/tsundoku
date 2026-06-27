import type { Meta, StoryObj } from '@storybook/vue3'
import ExtensionRow from './ExtensionRow.vue'
import { availableExtensions, installedExtensions } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for a single extension card. Flip the Storybook theme toolbar to
 * confirm both dark and light.
 */
const meta = {
  title: 'Settings/ExtensionRow',
  component: ExtensionRow,
  parameters: { layout: 'padded' },
  args: { busy: false },
} satisfies Meta<typeof ExtensionRow>

export default meta
type Story = StoryObj<typeof meta>

/** Installed, up to date — Uninstall only. */
export const Installed: Story = {
  args: { extension: installedExtensions[0]!, installed: true },
}

/** Installed with an update — UPDATE badge + Update + Uninstall. */
export const InstalledWithUpdate: Story = {
  args: { extension: installedExtensions[1]!, installed: true },
}

/** Available — the Install action. */
export const Available: Story = {
  args: { extension: availableExtensions[0]!, installed: false },
}

/** §16 busy — the acting button spins and the row dims/disables. */
export const Busy: Story = {
  args: { extension: installedExtensions[1]!, installed: true, busy: true },
}
