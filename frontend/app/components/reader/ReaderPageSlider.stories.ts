import type { Meta, StoryObj } from '@storybook/vue3'
import ReaderPageSlider from './ReaderPageSlider.vue'

/**
 * Stories for the reader's page slider. All four props are REQUIRED (no
 * defaults on the component), so the baseline set lives in `meta.args` per
 * this repo's typecheck-gate pattern; each story only overrides what it's
 * demonstrating. `seek` / `prev` / `next` are logged via Storybook actions.
 */
const meta = {
  title: 'Reader/ReaderPageSlider',
  component: ReaderPageSlider,
  parameters: { layout: 'padded' },
  decorators: [() => ({
    template: '<div style="max-width:420px;padding:16px;background:var(--bg);border:1px solid var(--border);border-radius:8px"><story /></div>',
  })],
  args: {
    page: 4,
    visiblePages: 12,
    hasPrev: true,
    hasNext: true,
  },
} satisfies Meta<typeof ReaderPageSlider>

export default meta
type Story = StoryObj<typeof meta>

/** A short chapter (12 pages) — at/below the tick threshold, so per-page dots render. */
export const FewPages: Story = {
  args: { page: 4, visiblePages: 12 },
}

/** A long chapter (165 pages, a real webtoon-length case) — ticks would smear
 *  into a solid bar above the threshold, so the track stays plain. */
export const ManyPages: Story = {
  args: { page: 80, visiblePages: 165 },
}

/** First page of the first chapter — the prev-chapter button is disabled (never hidden). */
export const AtFirstPage: Story = {
  args: { page: 0, visiblePages: 12, hasPrev: false },
}

/** Last page of the last chapter — the next-chapter button is disabled (never hidden). */
export const AtLastPage: Story = {
  args: { page: 11, visiblePages: 12, hasNext: false },
}
