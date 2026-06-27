import type { Meta, StoryObj } from '@storybook/vue3'
import Skeleton from './Skeleton.vue'

/**
 * Stories for the Skeleton placeholder. Each variant is shown in a width-framed
 * decorator so its shape reads; the shimmer sweep animates live.
 */
const meta = {
  title: 'UI/Skeleton',
  component: Skeleton,
  argTypes: {
    variant: { control: { type: 'inline-radio' }, options: ['card', 'row', 'line', 'cover'] },
    height: { control: { type: 'text' } },
  },
  args: { variant: 'line' },
  decorators: [
    () => ({ template: '<div style="width:280px"><story /></div>' }),
  ],
} satisfies Meta<typeof Skeleton>

export default meta
type Story = StoryObj<typeof meta>

/** A single text-line bar. */
export const Line: Story = {
  args: { variant: 'line' },
}

/** A list/table row block. */
export const Row: Story = {
  args: { variant: 'row' },
}

/** A card-sized block. */
export const Card: Story = {
  args: { variant: 'card' },
}

/** A manga-cover block (2:3 portrait). */
export const Cover: Story = {
  args: { variant: 'cover' },
  decorators: [
    () => ({ template: '<div style="width:160px"><story /></div>' }),
  ],
}

/** Every variant side by side. */
export const AllVariants: Story = {
  render: () => ({
    components: { Skeleton },
    template:
      '<div style="display:flex;align-items:flex-start;gap:20px;width:560px">' +
      '<div style="width:120px"><Skeleton variant="cover" /></div>' +
      '<div style="flex:1;display:flex;flex-direction:column;gap:12px">' +
      '<Skeleton variant="card" />' +
      '<Skeleton variant="row" />' +
      '<Skeleton variant="line" />' +
      '<Skeleton variant="line" height="8px" />' +
      '</div>' +
      '</div>',
  }),
}
