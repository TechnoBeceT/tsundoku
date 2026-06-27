import type { Meta, StoryObj } from '@storybook/vue3'
import LockedRow from './LockedRow.vue'

/**
 * Stories for LockedRow — a read-only "set at deploy time" key/value row. Shown
 * inside a card-width frame; the `Stack` story shows several rows as they'd sit
 * in a System card (the first row's top border reads as the group divider).
 */
const meta = {
  title: 'UI/LockedRow',
  component: LockedRow,
  argTypes: {
    label: { control: { type: 'text' } },
    value: { control: { type: 'text' } },
    muted: { control: { type: 'boolean' } },
  },
  args: { label: 'Storage folder', value: '/data/library' },
  decorators: [
    () => ({ template: '<div style="width:420px"><story /></div>' }),
  ],
} satisfies Meta<typeof LockedRow>

export default meta
type Story = StoryObj<typeof meta>

/** A single locked row. */
export const Default: Story = {}

/** A masked/dimmed value (e.g. a secret). */
export const Muted: Story = {
  args: { label: 'Password', value: '••••••••', muted: true },
}

/** Several rows stacked, as in a System / Engine card. */
export const Stack: Story = {
  render: () => ({
    components: { LockedRow },
    template:
      '<div style="width:420px">' +
      '<LockedRow label="Storage folder" value="/data/library" />' +
      '<LockedRow label="Server port" value="8080" />' +
      '<LockedRow label="Database" value="postgres://…/tsundoku" />' +
      '<LockedRow label="Password" value="••••••••" :muted="true" />' +
      '</div>',
  }),
}
