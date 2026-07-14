import type { Meta, StoryObj } from '@storybook/vue3'
import TrackerIcon from './TrackerIcon.vue'

/**
 * Stories for TrackerIcon — the small square brand logo shown next to a
 * tracker's name on the Settings → Trackers pane and the Series-Detail
 * inline Trackers section. Maps the registry id (MAL=1, AniList=2, Kitsu=3,
 * MangaUpdates=7) to its PNG under `public/tracker/`; an unknown id falls
 * back to a generic link glyph.
 */
const meta = {
  title: 'UI/TrackerIcon',
  component: TrackerIcon,
  parameters: { layout: 'padded' },
  args: { trackerId: 2 },
} satisfies Meta<typeof TrackerIcon>

export default meta
type Story = StoryObj<typeof meta>

/** AniList (id 2). */
export const AniList: Story = {
  args: { trackerId: 2 },
}

/** MyAnimeList (id 1). */
export const MyAnimeList: Story = {
  args: { trackerId: 1 },
}

/** Kitsu (id 3). */
export const Kitsu: Story = {
  args: { trackerId: 3 },
}

/** MangaUpdates (id 7). */
export const MangaUpdates: Story = {
  args: { trackerId: 7 },
}

/** An unregistered id falls back to a generic link glyph, never a broken image. */
export const UnknownFallback: Story = {
  args: { trackerId: 999 },
}

/** All four brand logos + the fallback, side by side for a quick visual diff. */
export const AllLogos: Story = {
  render: () => ({
    components: { TrackerIcon },
    template:
      '<div style="display:flex;align-items:center;gap:14px;flex-wrap:wrap">'
      + '<TrackerIcon :tracker-id="2" />'
      + '<TrackerIcon :tracker-id="1" />'
      + '<TrackerIcon :tracker-id="3" />'
      + '<TrackerIcon :tracker-id="7" />'
      + '<TrackerIcon :tracker-id="999" />'
      + '</div>',
  }),
}

/** A larger size (e.g. for a future richer card) proves the `size` prop scales cleanly. */
export const Large: Story = {
  args: { trackerId: 2, size: 32 },
}
