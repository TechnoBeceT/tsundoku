import type { Meta, StoryObj } from '@storybook/vue3'
import RepoRow from './RepoRow.vue'
import { repos } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for a single extension-repository row. Flip the Storybook theme
 * toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Settings/RepoRow',
  component: RepoRow,
  parameters: { layout: 'padded' },
  args: { canUp: true, canDown: true, busy: false },
} satisfies Meta<typeof RepoRow>

export default meta
type Story = StoryObj<typeof meta>

/** The pre-populated default repo — carries the DEFAULT pill. */
export const DefaultRepo: Story = {
  args: { repo: repos[0]!, canUp: false },
}

/** A user-added repo — removable, reorderable. */
export const Custom: Story = {
  args: { repo: repos[1]!, canDown: false },
}

/** §16 busy — the row dims and shows an inline spinner. */
export const Busy: Story = {
  args: { repo: repos[1]!, busy: true, canDown: false },
}
