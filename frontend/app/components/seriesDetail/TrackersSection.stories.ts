import type { Meta, StoryObj } from '@storybook/vue3'
import { userEvent, within } from 'storybook/test'
import { INITIAL_VIEWPORTS } from 'storybook/viewport'
import TrackersSection from './TrackersSection.vue'
import { trackBindingKitsu, trackBindings, trackSearchResults } from '../../fixtures/seriesDetail'
import type { TrackerStatus } from '../screens/settings.types'

/**
 * Stories for TrackersSection — the Series-Detail INLINE "Trackers" panel
 * (QCAT-234), replacing the retired PLANNED `MetadataSourcePicker` card and
 * the modal `TrackingDialog`. A DESIGN EXPLORATION for visual sign-off.
 *
 * Every state is a pure prop variation EXCEPT the ones that depend on local
 * UI state (which row's edit form is open, which tracker's search row is
 * expanded) — those use `play` functions to click through, the same pattern
 * `SeriesDetail.stories.ts`'s `DeleteDialogOpen` uses.
 *
 * Every required prop is driven via args/fixtures. Flip the theme toolbar to
 * confirm both dark and light.
 *
 * Every bound row and every "Add tracking" row renders its `TrackerIcon`
 * brand logo for free — `Default`/`Bound` already show AniList + MAL bound
 * and Kitsu + MangaUpdates offered to add, so all four brand logos are
 * visible without a dedicated icon-only story.
 */

// Two bound (AniList, MAL) + two connected-and-UNBOUND: Kitsu (supportsPrivate)
// and MangaUpdates (does NOT support private) — exercises both eye-toggle
// branches side by side in the "Add tracking" list.
const trackers: TrackerStatus[] = [
  { id: 2, name: 'AniList', needsOAuth: true, isLoggedIn: true, isTokenExpired: false, username: 'technobecet', supportsPrivate: true },
  { id: 1, name: 'MyAnimeList', needsOAuth: true, isLoggedIn: true, isTokenExpired: false, username: 'technobecet', supportsPrivate: false },
  { id: 3, name: 'Kitsu', needsOAuth: false, isLoggedIn: true, isTokenExpired: false, username: 'technobecet', supportsPrivate: true },
  { id: 7, name: 'MangaUpdates', needsOAuth: false, isLoggedIn: true, isTokenExpired: false, username: 'technobecet', supportsPrivate: false },
]

const meta = {
  title: 'SeriesDetail/TrackersSection',
  component: TrackersSection,
  parameters: { layout: 'padded' },
  args: {
    bindings: trackBindings,
    trackers,
    pending: false,
    searchResults: [],
    searching: false,
    binding: false,
  },
} satisfies Meta<typeof TrackersSection>

export default meta
type Story = StoryObj<typeof meta>

/** Default — two bound trackers (one in-progress, one completed+private) + Kitsu/MangaUpdates offered to add. */
export const Default: Story = {}

/** Empty — nothing bound yet; the "Add tracking" rows themselves ARE the empty view (Komikku's shape), no separate "nothing bound" message. */
export const Empty: Story = {
  args: { bindings: [] },
}

/** Nothing to show at all — no bindings AND no connected-and-unbound tracker (every tracker already bound or disconnected) → the EmptyState atom. */
export const NothingConnected: Story = {
  args: {
    bindings: [],
    trackers: trackers.map((t) => ({ ...t, isLoggedIn: false })),
  },
}

/** Loading — the bindings-list skeleton. */
export const Pending: Story = {
  args: { pending: true },
}

/** Bound — the plain populated list, edit forms closed (named per the brief's required-states list). */
export const Bound: Story = {}

/**
 * Editing — click a bound row's Edit (pencil) icon to open its inline
 * status/last-chapter-read/score/dates/private form live.
 */
export const Editing: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: 'Edit AniList entry' }))
  },
}

/** §16: a failed manual edit — open Edit to see the inline error. */
export const UpdateFailed: Story = {
  args: { updateError: 'AniList rejected the update — try again.' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: 'Edit AniList entry' }))
  },
}

/**
 * SearchResults — click "Kitsu" in the Add-tracking list to expand its
 * per-tracker search row, revealing the rich `TrackerSearchResultCard` list
 * (searchResults is pre-populated via args, so no query needs to be typed).
 */
export const SearchResults: Story = {
  args: { searchResults: trackSearchResults },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Kitsu/ }))
  },
}

/**
 * PrivateSupported — Kitsu supports private entries: expanding its
 * Add-tracking row shows the eye/eye-off toggle beside the search field.
 */
export const PrivateSupported: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Kitsu/ }))
  },
}

/**
 * PrivateUnsupported — MangaUpdates has no remote "private" concept
 * (`supportsPrivate: false`): expanding its row shows NO eye toggle, only the
 * search field + button.
 */
export const PrivateUnsupported: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /MangaUpdates/ }))
  },
}

/** A bind POST in flight — every search-result card's Bind button spins. */
export const Binding: Story = {
  args: { binding: true, searchResults: trackSearchResults },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Kitsu/ }))
  },
}

/** §16/bug 2: a failed search — open Kitsu's "Add tracking" row to see the inline error near the search box. */
export const SearchError: Story = {
  args: { searchError: 'anilist: rate limited — try again shortly.' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Kitsu/ }))
  },
}

/** §16/bug 2: a failed bind — open Kitsu's row with results showing and see the error under the search area. */
export const BindError: Story = {
  args: { searchResults: trackSearchResults, bindError: 'kitsu: entry already tracked by another series.' },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Kitsu/ }))
  },
}

/** §16/bug 2: a failed unbind — surfaced under the AniList row itself (no per-row "open" state to attach it to). */
export const UnbindError: Story = {
  args: { unbindError: 'anilist: could not delete the remote entry — 502.', unbindErrorId: trackBindings[0]!.id },
}

/** §16/bug 2: a failed remote refresh — surfaced under the AniList row itself. */
export const RefreshError: Story = {
  args: { refreshError: 'anilist: entry not found — it may have been deleted remotely.', refreshErrorId: trackBindings[0]!.id },
}

/** "Sync now" in flight — the header button spins. */
export const Syncing: Story = {
  args: { syncing: true },
}

/** §16: the last "Sync now" failed — surfaced above the bound-tracker list. */
export const SyncFailed: Story = {
  args: { syncError: 'Could not reach AniList — sync will retry automatically.' },
}

/** A third, Kitsu-shaped binding proves the score-scale fix survives composition into the section (see TrackerBindingRow's own stories for the isolated case). */
export const ScoreFormatKitsu: Story = {
  args: { bindings: [...trackBindings, trackBindingKitsu] },
}

/**
 * Real mobile viewport (QCAT-230/231) — the Add-tracking search row's
 * `@media (max-width: 900px)` rule stacks the search field full-width above
 * the eye toggle + Search button, with no horizontal overflow.
 */
export const MobileViewport: Story = {
  parameters: {
    viewport: { options: INITIAL_VIEWPORTS },
  },
  globals: {
    viewport: { value: 'iphone12', isRotated: false },
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement)
    await userEvent.click(await canvas.findByRole('button', { name: /Kitsu/ }))
  },
}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so the panel renders
 * light regardless of the toolbar. Args-driven; no interaction.
 */
export const LightTheme: Story = {
  render: (args) => ({
    components: { TrackersSection },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="min-height:100vh;background:var(--bg);padding:20px"><TrackersSection v-bind="args" /></div>',
  }),
}
