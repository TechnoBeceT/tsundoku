import type { Meta, StoryObj } from '@storybook/vue3'
import LibraryPane from './LibraryPane.vue'
import { librarySettings, systemInfo } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Library pane (Schedules & Behavior + read-only System). Flip
 * the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Settings/LibraryPane',
  component: LibraryPane,
  parameters: { layout: 'padded' },
  args: {
    library: librarySettings,
    system: systemInfo,
    autoIdentify: true,
    autoIdentifyBusy: false,
  },
} satisfies Meta<typeof LibraryPane>

export default meta
type Story = StoryObj<typeof meta>

/** Idle — nothing edited yet, so the Save button is disabled. */
export const Default: Story = {
  args: { save: { status: 'idle' } },
}

/** §16 saving — the Save button spins while the persist call is in flight. */
export const Saving: Story = {
  args: { save: { status: 'saving' } },
}

/** §16 error — a visible, specific failure message beside the Save button. */
export const SaveError: Story = {
  args: { save: { status: 'error', message: 'Save failed — refresh interval must be at least 10m.' } },
}

/** Auto-identify off — the owner has paused the background metadata-engine pass. */
export const AutoIdentifyOff: Story = {
  args: { save: { status: 'idle' }, autoIdentify: false },
}

/** Auto-identify toggle busy — its own save is in flight (disabled while saving). */
export const AutoIdentifyBusy: Story = {
  args: { save: { status: 'idle' }, autoIdentifyBusy: true },
}
