import type { Meta, StoryObj } from '@storybook/vue3'
import { expect, userEvent, within } from 'storybook/test'
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

/**
 * M2 bugfix: switching to Available and typing a query narrows the grid down
 * to the matching extension(s) — proves the search box actually filters
 * rather than just existing cosmetically.
 */
export const SearchFiltersAvailable: Story = {
  args: { extensionAction: { busyId: null }, repoAction: { busyId: null } },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)

    await userEvent.click(await canvas.findByRole('tab', { name: /Available/ }))
    await canvas.findByText('Reaper Scans')

    const search = await canvas.findByPlaceholderText('Search extensions by name or language…')
    await userEvent.type(search, 'kakao')

    await canvas.findByText('Kakao')
    await expect(canvas.queryByText('Reaper Scans')).not.toBeInTheDocument()
    await expect(canvas.queryByText('Flame Comics')).not.toBeInTheDocument()
  },
}
