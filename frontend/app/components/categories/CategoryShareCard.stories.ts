import type { Meta, StoryObj } from '@storybook/vue3'
import CategoryShareCard from './CategoryShareCard.vue'

/**
 * Stories for CategoryShareCard — one category card from the Categories overview.
 * Each card is constrained to a single grid column width so the layout matches its
 * in-screen home. Flip the Storybook theme toolbar to confirm the chip, count, and
 * share bar all re-tint from the tokens in both themes.
 */
const meta = {
  title: 'Categories/CategoryShareCard',
  component: CategoryShareCard,
  parameters: { layout: 'centered' },
  argTypes: {
    share: { control: { type: 'range', min: 0, max: 100, step: 1 } },
    onOpen: { action: 'open' },
  },
  args: { name: 'Manhwa', count: 14, share: 42 },
  decorators: [
    () => ({ template: '<div style="width:260px"><story /></div>' }),
  ],
} satisfies Meta<typeof CategoryShareCard>

export default meta
type Story = StoryObj<typeof meta>

/** A populated category with a healthy share of the library. */
export const Default: Story = {
  args: { name: 'Manhwa', count: 14, share: 42 },
}

/** A zero-count category — still a selectable card at 0%. */
export const Empty: Story = {
  args: { name: 'Comic', count: 0, share: 0 },
}

/** A category that dominates the library. */
export const Dominant: Story = {
  args: { name: 'Manga', count: 88, share: 100 },
}
