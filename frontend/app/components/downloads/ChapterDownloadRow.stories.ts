import type { Meta, StoryObj } from '@storybook/vue3'
import ChapterDownloadRow from './ChapterDownloadRow.vue'
import { downloadItems } from '../../fixtures/downloads'

/**
 * Stories for ChapterDownloadRow — the shared download-activity row used by all
 * three Downloads tabs. Covers the cover image vs the branded placeholder, the
 * category chip + meta line, the chapter-state badge, and the `before-badge`
 * slot (where each tab injects its trailing content). Flip the theme toolbar to
 * confirm the token-only palette reads on both surfaces.
 */
const meta = {
  title: 'Downloads/ChapterDownloadRow',
  component: ChapterDownloadRow,
  parameters: { layout: 'padded' },
} satisfies Meta<typeof ChapterDownloadRow>

export default meta
type Story = StoryObj<typeof meta>

// A row with a real cover (Solo Leveling, downloading).
const withCover = downloadItems[0]!
// A row whose cover is empty → the branded placeholder (Berserk, upgrading).
const noCover = downloadItems[1]!

/** Default row with a cover image and the downloading badge. */
export const Default: Story = {
  args: { item: withCover },
}

/** Empty cover → the inverse BrandMark placeholder. */
export const PlaceholderCover: Story = {
  args: { item: noCover },
}

/** With a `before-badge` slot — the Active tab's indeterminate progress bar. */
export const WithProgressSlot: Story = {
  render: (args) => ({
    components: { ChapterDownloadRow },
    setup: () => ({ args }),
    template: `
      <ChapterDownloadRow v-bind="args">
        <template #before-badge>
          <div style="width:90px;height:5px;border-radius:var(--radius-pill);background:var(--surface3);flex:none" />
        </template>
      </ChapterDownloadRow>
    `,
  }),
  args: { item: withCover },
}
