import type { Meta, StoryObj } from '@storybook/vue3'
import NotificationsPane from './NotificationsPane.vue'
// Load this screen's status tokens directly: index.css does not @import them yet
// (a coordinator wires that line to avoid parallel-worker conflicts), so the
// side-effect import keeps every story rendering with the real palette.
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Notifications pane. Flip the Storybook theme toolbar to
 * confirm both dark and light. Covers every honest per-device state (default /
 * granted / blocked / unsupported) plus the busy + error states of both actions.
 */
const meta = {
  title: 'Settings/NotificationsPane',
  component: NotificationsPane,
  parameters: { layout: 'padded' },
  args: { state: 'default', globalEnabled: true },
} satisfies Meta<typeof NotificationsPane>

export default meta
type Story = StoryObj<typeof meta>

/** Default — supported + not yet enabled on this device (the Enable button). */
export const Default: Story = {}

/** Granted — subscribed on this device (the Disable button). */
export const Granted: Story = {
  args: { state: 'granted' },
}

/** Blocked — the owner denied the permission (re-enable in browser settings). */
export const Blocked: Story = {
  args: { state: 'blocked' },
}

/** Unsupported — the browser lacks Web Push. */
export const Unsupported: Story = {
  args: { state: 'unsupported' },
}

/** Global off — the master switch disabled (no device notified). */
export const GlobalOff: Story = {
  args: { globalEnabled: false },
}

/** Busy — the per-device enable action is in flight (button spins). */
export const EnableBusy: Story = {
  args: { busy: true },
}

/** Error — a per-device action failed (inline message) + a global-toggle error. */
export const Errors: Story = {
  args: {
    state: 'default',
    error: 'Could not register this device for notifications',
    globalError: 'Could not save the notifications setting',
  },
}
