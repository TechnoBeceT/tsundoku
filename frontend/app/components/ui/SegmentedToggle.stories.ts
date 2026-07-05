import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import SegmentedToggle from './SegmentedToggle.vue'

/**
 * Stories for the SegmentedToggle. The interactive stories wire a local ref so
 * clicking a segment moves the accent fill; a two-option (Popular/Latest) and a
 * three-option variant are shown. Flip the theme toolbar to confirm both themes.
 */
const meta = {
  title: 'UI/SegmentedToggle',
  component: SegmentedToggle,
  // modelValue + options are required props; each story renders its own live-ref
  // wrapper, so these defaults only satisfy the CSF3 story typing.
  args: {
    modelValue: 'popular',
    options: [
      { key: 'popular', label: 'Popular' },
      { key: 'latest', label: 'Latest' },
    ],
  },
} satisfies Meta<typeof SegmentedToggle>

export default meta
type Story = StoryObj<typeof meta>

/** Two options (the Discover Popular / Latest switch). */
export const TwoOptions: Story = {
  render: () => ({
    components: { SegmentedToggle },
    setup: () => ({
      value: ref('popular'),
      options: [
        { key: 'popular', label: 'Popular' },
        { key: 'latest', label: 'Latest' },
      ],
    }),
    template: '<SegmentedToggle v-model="value" :options="options" />',
  }),
}

/** Three options. */
export const ThreeOptions: Story = {
  render: () => ({
    components: { SegmentedToggle },
    setup: () => ({
      value: ref('all'),
      options: [
        { key: 'all', label: 'All' },
        { key: 'manga', label: 'Manga' },
        { key: 'manhwa', label: 'Manhwa' },
      ],
    }),
    template: '<SegmentedToggle v-model="value" :options="options" />',
  }),
}
