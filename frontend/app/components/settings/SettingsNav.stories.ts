import type { Meta, StoryObj } from '@storybook/vue3'
import SettingsNav from './SettingsNav.vue'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Settings sidebar nav. Flip the Storybook theme toolbar to
 * confirm the active highlight reads in both dark and light.
 */
const meta = {
  title: 'Settings/SettingsNav',
  component: SettingsNav,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof SettingsNav>

export default meta
type Story = StoryObj<typeof meta>

/** The Library pane selected (the default landing). */
export const Library: Story = {
  args: { active: 'library' },
}

/** The Extensions pane selected. */
export const Extensions: Story = {
  args: { active: 'extensions' },
}
