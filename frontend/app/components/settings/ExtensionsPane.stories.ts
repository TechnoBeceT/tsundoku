import type { Meta, StoryObj } from '@storybook/vue3'
import ExtensionsPane from './ExtensionsPane.vue'
import { availableExtensions, extCheckInterval, installedExtensions, repos } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Sources & Extensions pane (Installed / Available / Repos). Flip
 * the Storybook theme toolbar to confirm both dark and light. The destructive
 * uninstall confirm opens from an installed row's Uninstall button.
 */
const meta = {
  title: 'Settings/ExtensionsPane',
  component: ExtensionsPane,
  parameters: { layout: 'padded' },
  args: {
    extensions: installedExtensions,
    availableExtensions,
    repos,
    extCheckInterval,
  },
} satisfies Meta<typeof ExtensionsPane>

export default meta
type Story = StoryObj<typeof meta>

/** Installed segment — two rows carry an available UPDATE badge. */
export const Default: Story = {
  args: { extensionAction: { busyId: null }, repoAction: { busyId: null } },
}

/**
 * §16: one row mid-update (busy spinner + disabled) and a pane-level failure
 * banner — the per-row mutation no longer fires into the void.
 */
export const Busy: Story = {
  args: {
    extensionAction: { busyId: 'asurascans', error: 'Update failed — 502 from the extension repository.' },
  },
}
