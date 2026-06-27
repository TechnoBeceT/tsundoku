import type { Meta, StoryObj } from '@storybook/vue3'
import StatusBadge from './StatusBadge.vue'
import type { ChapterState } from './types'

/**
 * Stories for the chapter-state StatusBadge. The `All` story shows every one of
 * the seven states; flip the Storybook theme toolbar to confirm the
 * theme-independent palette reads on both surfaces.
 */
const meta = {
  title: 'UI/StatusBadge',
  component: StatusBadge,
  argTypes: {
    state: {
      control: { type: 'select' },
      options: [
        'wanted',
        'downloading',
        'downloaded',
        'upgrade_available',
        'upgrading',
        'failed',
        'permanently_failed',
      ],
    },
  },
  args: { state: 'downloading' },
} satisfies Meta<typeof StatusBadge>

export default meta
type Story = StoryObj<typeof meta>

/** A single badge driven by the `state` control. */
export const Playground: Story = {}

const ALL_STATES: ChapterState[] = [
  'wanted',
  'downloading',
  'downloaded',
  'upgrade_available',
  'upgrading',
  'failed',
  'permanently_failed',
]

/** Every one of the seven chapter states. */
export const All: Story = {
  render: () => ({
    components: { StatusBadge },
    setup: () => ({ states: ALL_STATES }),
    template:
      '<div style="display:flex;flex-wrap:wrap;gap:10px">' +
      '<StatusBadge v-for="s in states" :key="s" :state="s" />' +
      '</div>',
  }),
}
