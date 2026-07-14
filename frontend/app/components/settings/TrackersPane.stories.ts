import type { Meta, StoryObj } from '@storybook/vue3'
import TrackersPane from './TrackersPane.vue'
import { trackers } from '../../fixtures/settings'
import '../../assets/css/tokens/settings.css'

/**
 * Stories for the Settings → Trackers pane (Phase 3d, owner-delegated — built
 * to spec without a design sign-off; these are the minimal states the gate
 * asks for). Flip the Storybook theme toolbar to confirm both dark and light.
 */
const meta = {
  title: 'Settings/TrackersPane',
  component: TrackersPane,
  parameters: { layout: 'padded' },
  args: { trackers },
} satisfies Meta<typeof TrackersPane>

export default meta
type Story = StoryObj<typeof meta>

/** One connected (AniList), one disconnected OAuth (MAL), two credential trackers (Kitsu/MangaUpdates). */
export const Populated: Story = {}

/** Loading — the pane's own skeleton rows while the list fetches. */
export const Pending: Story = {
  args: { trackers: [], pending: true },
}

/** §16: a connect/login/logout failure surfaces as one pane-level message (mirrors ExtensionsPane). */
export const ActionFailed: Story = {
  args: { trackerAction: { busyId: null, error: 'Could not reach AniList — try again.' } },
}

/** An OAuth tracker with no client-id configured shows the redirect URL to register instead of a dead-end button. */
export const Misconfigured: Story = {
  args: {
    misconfiguredIds: [1],
    redirectUrl: 'https://tsundoku.example.com/auth/tracker/callback',
  },
}

/** A connect/login action in flight — the acting row's button spins. */
export const Connecting: Story = {
  args: { trackerAction: { busyId: 1 } },
}
