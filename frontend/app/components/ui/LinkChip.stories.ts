import type { Meta, StoryObj } from '@storybook/vue3'
import LinkChip from './LinkChip.vue'

/**
 * Stories for LinkChip. Each known site derives its own icon from the label; an
 * explicit `icon` overrides it, an unknown label falls back to the generic link
 * glyph, and a non-http(s) URL renders the inert dimmed state (no live link).
 * Hover a valid chip to see the trailing arrow slide in.
 */
const meta = {
  title: 'UI/LinkChip',
  component: LinkChip,
  args: { label: 'AniList', url: 'https://anilist.co/manga/105398' },
} satisfies Meta<typeof LinkChip>

export default meta
type Story = StoryObj<typeof meta>

/** A known tracker — icon derived from the label. */
export const AniList: Story = {}

/** Another known site (book-open glyph). */
export const MangaDex: Story = {
  args: { label: 'MangaDex', url: 'https://mangadex.org/title/32d76d19' },
}

/** The publisher's own page — badge-check glyph. */
export const Official: Story = {
  args: { label: 'Official', url: 'https://www.webtoons.com/en/action/solo-leveling' },
}

/** Unknown label → the generic external-link fallback glyph. */
export const UnknownSite: Story = {
  args: { label: 'Fan Wiki', url: 'https://sololeveling.fandom.com' },
}

/** Explicit `icon` overrides the derived one. */
export const ExplicitIcon: Story = {
  args: { label: 'Discussion', url: 'https://reddit.com/r/sololeveling', icon: 'lucide:messages-square' },
}

/** A non-http(s) URL renders inert + dimmed — never a live link. */
export const InvalidUrl: Story = {
  args: { label: 'Broken', url: 'javascript:alert(1)' },
}

/** Several chips together (the way LinksRow lays them out). */
export const Row: Story = {
  render: () => ({
    components: { LinkChip },
    template:
      '<div style="display:flex;gap:8px;flex-wrap:wrap">' +
      '<LinkChip label="AniList" url="https://anilist.co/manga/105398" />' +
      '<LinkChip label="MangaDex" url="https://mangadex.org/title/32d76d19" />' +
      '<LinkChip label="MangaUpdates" url="https://www.mangaupdates.com/series/abc" />' +
      '<LinkChip label="Official" url="https://www.webtoons.com/en/action/solo-leveling" />' +
      '</div>',
  }),
}
