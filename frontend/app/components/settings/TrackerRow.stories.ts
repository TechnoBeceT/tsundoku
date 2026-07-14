import type { Meta, StoryObj } from '@storybook/vue3'
import TrackerRow from './TrackerRow.vue'
import { trackers } from '../../fixtures/settings'

/**
 * Stories for TrackerRow — one tracker's connect card on the Settings →
 * Trackers pane. Each state below picks a different fixture row / prop combo
 * to exercise the three mutually-exclusive shapes (connected, disconnected
 * OAuth, disconnected credential-based) plus the brand-logo (`TrackerIcon`)
 * head row shared across all of them.
 */
const meta = {
  title: 'Settings/TrackerRow',
  component: TrackerRow,
  parameters: { layout: 'padded' },
  args: {
    tracker: trackers[0]!,
  },
} satisfies Meta<typeof TrackerRow>

export default meta
type Story = StoryObj<typeof meta>

/** Connected (AniList) — logo + name + "Connected" tag + Disconnect. */
export const Connected: Story = {}

/** Disconnected, OAuth-based (MyAnimeList) — logo + name + Connect button. */
export const DisconnectedOAuth: Story = {
  args: { tracker: trackers[1]! },
}

/** Disconnected, credential-based (Kitsu) — logo + name + inline sign-in form. */
export const DisconnectedCredentials: Story = {
  args: { tracker: trackers[2]! },
}

/** Disconnected, credential-based (MangaUpdates) — the fourth brand logo. */
export const MangaUpdates: Story = {
  args: { tracker: trackers[3]! },
}

/** A connected account whose token has expired — "Token expired" tag. */
export const TokenExpired: Story = {
  args: {
    tracker: { ...trackers[0]!, isTokenExpired: true },
  },
}

/** An OAuth tracker with no client-id configured — shows the redirect URL instead of a dead-end Connect button. */
export const Misconfigured: Story = {
  args: {
    tracker: trackers[1]!,
    misconfigured: true,
    redirectUrl: 'https://tsundoku.example.com/auth/tracker/callback',
  },
}

/** This row's own connect/login/logout action in flight — button spins. */
export const Busy: Story = {
  args: {
    tracker: trackers[0]!,
    busy: true,
  },
}

/** All four trackers stacked — a quick visual diff of every brand logo together. */
export const AllTrackers: Story = {
  render: () => ({
    components: { TrackerRow },
    setup: () => ({ trackers }),
    template:
      '<div style="display:flex;flex-direction:column;gap:10px">'
      + '<TrackerRow v-for="t in trackers" :key="t.id" :tracker="t" />'
      + '</div>',
  }),
}
