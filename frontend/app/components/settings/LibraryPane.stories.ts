import type { Meta, StoryObj } from '@storybook/vue3'
import LibraryPane from './LibraryPane.vue'
import { systemInfo } from '../../fixtures/settings'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Library pane after download-engine controls move to their
 * canonical pane: library metadata behavior + read-only deploy facts remain.
 */
const meta = {
  title: 'Settings/LibraryPane',
  component: LibraryPane,
  parameters: { layout: 'padded' },
  args: {
    system: systemInfo,
    autoIdentify: true,
    autoIdentifyBusy: false,
  },
} satisfies Meta<typeof LibraryPane>

export default meta
type Story = StoryObj<typeof meta>

/** Metadata behavior and read-only system facts stay outside Download engine. */
export const Default: Story = {
}

/** Auto-identify off — the owner has paused the background metadata-engine pass. */
export const AutoIdentifyOff: Story = {
  args: { autoIdentify: false },
}

/** Auto-identify toggle busy — its own save is in flight (disabled while saving). */
export const AutoIdentifyBusy: Story = {
  args: { autoIdentifyBusy: true },
}
