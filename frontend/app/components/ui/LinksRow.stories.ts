import type { Meta, StoryObj } from '@storybook/vue3'
import LinksRow from './LinksRow.vue'
import type { SeriesLink } from '../screens/seriesDetail.types'

/**
 * Stories for LinksRow — the series links row. Drives the layout via the `links`
 * arg (never hardcoded markup) across a typical set, a single link, a wrapping
 * many-links set, and the empty case (renders nothing).
 */
const links: SeriesLink[] = [
  { label: 'AniList', url: 'https://anilist.co/manga/105398' },
  { label: 'MangaDex', url: 'https://mangadex.org/title/32d76d19' },
  { label: 'MangaUpdates', url: 'https://www.mangaupdates.com/series/abc' },
  { label: 'Official', url: 'https://www.webtoons.com/en/action/solo-leveling' },
]

const meta = {
  title: 'UI/LinksRow',
  component: LinksRow,
  args: { links },
} satisfies Meta<typeof LinksRow>

export default meta
type Story = StoryObj<typeof meta>

/** The common tracker + official set. */
export const Default: Story = {}

/** A lone official link. */
export const Single: Story = {
  args: { links: [{ label: 'Official', url: 'https://www.webtoons.com/en/action/solo-leveling' }] },
}

/** Many links wrapping onto multiple rows in a narrow column. */
export const ManyWrapping: Story = {
  args: {
    links: [
      ...links,
      { label: 'Anime-Planet', url: 'https://www.anime-planet.com/manga/solo-leveling' },
      { label: 'MyAnimeList', url: 'https://myanimelist.net/manga/121496' },
      { label: 'Kitsu', url: 'https://kitsu.io/manga/solo-leveling' },
      { label: 'Fan Wiki', url: 'https://sololeveling.fandom.com' },
    ],
  },
  render: (args) => ({
    components: { LinksRow },
    setup: () => ({ args }),
    template: '<div style="max-width:360px"><LinksRow v-bind="args" /></div>',
  }),
}

/** Empty list — the row renders nothing at all. */
export const Empty: Story = {
  args: { links: [] },
}
