import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import TrackerBindingRow from './TrackerBindingRow.vue'
import { trackBindingKitsu, trackBindings } from '../../fixtures/seriesDetail'
import type { TrackBinding } from '../screens/seriesDetail.types'

/**
 * Stories for TrackerBindingRow — one bound tracker's row + its inline
 * manual-edit form (extracted from the retired `TrackingDialog`, QCAT-234).
 * `editing` is now a PARENT-OWNED prop (the section enforces "one row open
 * at a time" across the whole bound list), so the interactive `EditState`/
 * `ScoreFormat*` stories wrap the component in a tiny local-`ref` render —
 * click the pencil icon to open the form live, mirroring how the section
 * itself will drive it.
 *
 * Every story renders the row's `TrackerIcon` brand logo beside the tracker
 * name for free (no extra states needed) — `Default` uses the AniList
 * fixture, `CompletedPrivate` the MAL fixture, `ScoreFormatKitsu` the Kitsu
 * fixture, so all three brand logos are already visible across this file.
 */
const meta = {
  title: 'SeriesDetail/TrackerBindingRow',
  component: TrackerBindingRow,
  parameters: { layout: 'padded' },
  args: {
    binding: trackBindings[0]!,
  },
} satisfies Meta<typeof TrackerBindingRow>

export default meta
type Story = StoryObj<typeof meta>

/** A live wrapper: local `editing` state toggled by the row's own emits — the
 *  same "at most one row open" shape TrackersSection drives it with. */
function interactive(binding: TrackBinding) {
  return {
    components: { TrackerBindingRow },
    setup: () => {
      const editing = ref(false)
      return { binding, editing }
    },
    template: `
      <TrackerBindingRow
        :binding="binding"
        :editing="editing"
        @toggle-edit="editing = !editing"
        @cancel-edit="editing = false"
        @submit="editing = false"
      />
    `,
  }
}

/** Default — a bound row, edit form closed. */
export const Default: Story = {}

/** A completed + private entry (the fixture's MAL binding). */
export const CompletedPrivate: Story = {
  args: { binding: trackBindings[1]! },
}

/** EditState — click the Edit (pencil) icon to open the inline form live. */
export const EditState: Story = {
  render: () => interactive(trackBindings[0]!),
}

/**
 * ScoreFormatPoint100 — the AniList fixture's score is 92 on its OWN native
 * POINT_100 (0-100) scale. Click Edit: the Score field renders a 0-100
 * slider seeded at 92 (not a fixed 0-10 control writing 9/100).
 */
export const ScoreFormatPoint100: Story = {
  render: () => interactive(trackBindings[0]!),
}

/**
 * ScoreFormatKitsu — Kitsu's native scale (KITSU_RATING_TWENTY, 0-20) maps to
 * the closest ScoreSelector shape, `point10decimal` (0-10, 0.5 steps).
 * Click Edit: the fixture's 17/20 native should show 8.5, never 17 or 9.
 */
export const ScoreFormatKitsu: Story = {
  render: () => interactive(trackBindingKitsu),
}

/** §16: a failed manual edit — open Edit to see the inline error. */
export const UpdateFailed: Story = {
  args: { editing: true, updateError: 'AniList rejected the update — try again.' },
}

/** A manual edit in flight — the Save button spins, fields disabled. */
export const Updating: Story = {
  args: { editing: true, updateBusy: true },
}

/** Unbind in flight — the Unbind button spins. */
export const Unbinding: Story = {
  args: { unbindBusy: true },
}

/** Refresh (remote re-pull) in flight — the Refresh icon button disables. */
export const Refreshing: Story = {
  args: { refreshBusy: true },
}

/**
 * Light theme — pinned via a `data-theme="light"` subtree so the row renders
 * light regardless of the toolbar toggle.
 */
export const LightTheme: Story = {
  render: (args) => ({
    components: { TrackerBindingRow },
    setup: () => ({ args }),
    template:
      '<div data-theme="light" style="padding:20px;background:var(--bg)"><TrackerBindingRow v-bind="args" /></div>',
  }),
}
