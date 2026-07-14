import type { Meta, StoryObj } from '@storybook/vue3'
import TrackingDialog from './TrackingDialog.vue'
import { trackBindingKitsu, trackBindings, trackSearchResults } from '../../fixtures/seriesDetail'
import type { TrackerStatus } from '../screens/settings.types'

/**
 * Stories for TrackingDialog — the Series-Detail "Trackers" panel: bound
 * trackers (with the Phase 4 per-row Edit form + Sync now) plus the
 * "Add tracker" search+bind flow. A DESIGN EXPLORATION for visual sign-off. NO
 * `play` interaction is used (and none could reach it: reka portals the dialog
 * to `document.body`, outside the story canvas — mirrors CoverPickerModal/
 * MetadataIdentifyModal). Every state is a pure prop variation; the Edit form
 * and the "Add tracker" search step are LOCAL UI state — click "Edit" on a
 * bound row, or pick a tracker + search, to see them live.
 *
 * Every required prop is driven via args/fixtures. Flip the theme toolbar to
 * confirm both dark and light.
 */

// Two connected (AniList + MAL, both already bound in trackBindings) plus one
// connected-and-UNBOUND (Kitsu — the only one offered in "Add tracker").
const trackers: TrackerStatus[] = [
  { id: 2, name: 'AniList', needsOAuth: true, isLoggedIn: true, isTokenExpired: false, username: 'technobecet' },
  { id: 1, name: 'MyAnimeList', needsOAuth: true, isLoggedIn: true, isTokenExpired: false, username: 'technobecet' },
  { id: 3, name: 'Kitsu', needsOAuth: false, isLoggedIn: true, isTokenExpired: false, username: 'technobecet' },
]

const meta = {
  title: 'SeriesDetail/TrackingDialog',
  component: TrackingDialog,
  parameters: { layout: 'fullscreen' },
  args: {
    open: true,
    bindings: trackBindings,
    trackers,
    pending: false,
    searchResults: [],
    searching: false,
    binding: false,
  },
} satisfies Meta<typeof TrackingDialog>

export default meta
type Story = StoryObj<typeof meta>

/** Default — two bound trackers (one in-progress, one completed+private) + Kitsu offered to add. */
export const Default: Story = {}

/** Loading — the bindings-list skeleton. */
export const Pending: Story = {
  args: { pending: true },
}

/** Empty — no trackers bound yet (§16 empty state); every connected tracker is offered. */
export const Empty: Story = {
  args: { bindings: [] },
}

/**
 * EditState — same populated bindings; click a row's Edit (pencil) icon to open
 * the inline status/last-chapter-read/score/dates/private form live (local UI
 * state, not prop-driven — mirrors MetadataIdentifyModal's "Selected" story).
 */
export const EditState: Story = {}

/**
 * ScoreFormatPoint100 — the score-scale fix (spec: per-binding `scoreFormat`).
 * The AniList row's fixture score is 92 on its OWN native POINT_100 (0-100)
 * scale, NOT a 0-10 value. Click its Edit (pencil) icon: the Score field
 * renders a 0-100 slider seeded at 92 — the OLD behaviour rendered a fixed
 * 0-10 control here and would have silently written back 9/100 instead.
 */
export const ScoreFormatPoint100: Story = {}

/**
 * ScoreFormatKitsu — Kitsu's native scale (KITSU_RATING_TWENTY) is 0-20, which
 * no ScoreSelector shape spans directly; `scoreSelectorFormat` maps it to the
 * closest fit, `point10decimal` (0-10, 0.5 steps — matches Kitsu's own web UI
 * convention), and `scoreToDisplay`/`scoreToNative` convert the value at the
 * boundary. Click the Kitsu row's Edit icon: its fixture score is 17/20
 * native, so the slider should show 8.5 — never 17 (which would overflow the
 * 0-10 control) or 9 (rounding, not the real halved value).
 */
export const ScoreFormatKitsu: Story = {
  args: { bindings: [...trackBindings, trackBindingKitsu] },
}

/** §16: a failed manual edit — open a row's Edit form to see the inline error (updateError is prop-driven; the form itself is opened by hand). */
export const UpdateFailed: Story = {
  args: { updateError: 'AniList rejected the update — try again.' },
}

/** A manual edit in flight — open Edit on the AniList row to see its Save button spin. */
export const Updating: Story = {
  args: { updateBusyId: trackBindings[0]!.id },
}

/** "Sync now" in flight — the footer button spins. */
export const Syncing: Story = {
  args: { syncing: true },
}

/** §16: the last "Sync now" failed — surfaced above the bound-tracker list. */
export const SyncFailed: Story = {
  args: { syncError: 'Could not reach AniList — sync will retry automatically.' },
}

/** AddTracker — populated "Add tracker" search results for Kitsu (the one addable tracker). */
export const AddTrackerResults: Story = {
  args: { searchResults: trackSearchResults },
}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so the panel renders
 * light regardless of the toolbar. Args-driven; no interaction.
 */
export const LightTheme: Story = {
  render: (args) => ({
    components: { TrackingDialog },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="min-height:100vh;background:var(--bg)"><TrackingDialog v-bind="args" /></div>',
  }),
}
