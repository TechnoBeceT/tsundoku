import type { Meta, StoryObj } from '@storybook/vue3'
import ChipRow from './ChipRow.vue'

/**
 * Stories for ChipRow — a labelled, wrapping row of Chips. Driven entirely via
 * args: a labelled genre row, an accent-variant tag row, a label-less row, a
 * long wrapping set, and the empty case (renders nothing).
 */
const meta = {
  title: 'UI/ChipRow',
  component: ChipRow,
  argTypes: {
    variant: {
      control: { type: 'inline-radio' },
      options: ['neutral', 'category', 'language', 'accent', 'frost'],
    },
  },
  args: {
    label: 'Genres',
    variant: 'neutral',
    items: ['Action', 'Adventure', 'Fantasy', 'Shounen'],
  },
} satisfies Meta<typeof ChipRow>

export default meta
type Story = StoryObj<typeof meta>

/** A labelled genre row. */
export const Genres: Story = {}

/** Accent-tinted tags. */
export const Tags: Story = {
  args: { label: 'Tags', variant: 'accent', items: ['Overpowered MC', 'Dungeons', 'System', 'Reincarnation'] },
}

/** No label — just the chips. */
export const NoLabel: Story = {
  args: { label: undefined },
}

/** A long set wrapping across rows in a narrow column. */
export const ManyWrapping: Story = {
  args: {
    items: ['Action', 'Adventure', 'Comedy', 'Drama', 'Fantasy', 'Horror', 'Mystery', 'Psychological', 'Romance', 'Sci-Fi', 'Slice of Life', 'Supernatural'],
  },
  render: (args) => ({
    components: { ChipRow },
    setup: () => ({ args }),
    template: '<div style="max-width:320px"><ChipRow v-bind="args" /></div>',
  }),
}

/** Empty items — renders nothing. */
export const Empty: Story = {
  args: { items: [] },
}
