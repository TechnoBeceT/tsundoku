import type { Meta, StoryObj } from '@storybook/vue3'
import StatTile from './StatTile.vue'

/**
 * Stories for StatTile — a label + big number. Shows the default text tone, a
 * coloured tone, and a row of tiles as they'd sit in a summary strip.
 */
const meta = {
  title: 'UI/StatTile',
  component: StatTile,
  argTypes: {
    label: { control: { type: 'text' } },
    value: { control: { type: 'text' } },
    tone: { control: { type: 'text' } },
  },
  args: { label: 'On disk', value: 128 },
} satisfies Meta<typeof StatTile>

export default meta
type Story = StoryObj<typeof meta>

/** Default tile in the text tone. */
export const Default: Story = {}

/** Value tinted with a token tone (e.g. the emerald "on disk" accent). */
export const Tinted: Story = {
  args: { label: 'On disk', value: 128, tone: 'var(--sd-stat-disk)' },
}

/** A summary strip of tiles, the common composition. */
export const Strip: Story = {
  render: () => ({
    components: { StatTile },
    template:
      '<div style="display:flex;gap:36px">' +
      '<StatTile label="Series" :value="42" />' +
      '<StatTile label="On disk" :value="128" tone="var(--sd-stat-disk)" />' +
      '<StatTile label="Failed" :value="3" tone="var(--danger-text)" />' +
      '<StatTile label="Queued" :value="17" tone="var(--accentBright)" />' +
      '</div>',
  }),
}

/** Slot override for a custom value node. */
export const SlotValue: Story = {
  args: { label: 'Storage' },
  render: (args) => ({
    components: { StatTile },
    setup: () => ({ args }),
    template:
      '<StatTile v-bind="args"><span>2.4 <span style="font-size:var(--text-lg)">GB</span></span></StatTile>',
  }),
}
