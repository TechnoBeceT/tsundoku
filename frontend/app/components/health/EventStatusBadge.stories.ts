import type { Meta, StoryObj } from '@storybook/vue3'
import EventStatusBadge from './EventStatusBadge.vue'

/**
 * Stories for the event-outcome badge. Both outcomes ship a glyph + label so the
 * good/bad distinction never relies on colour alone. Flip the theme toolbar.
 */
const meta = {
  title: 'Health/EventStatusBadge',
  component: EventStatusBadge,
  parameters: { layout: 'centered' },
} satisfies Meta<typeof EventStatusBadge>

export default meta
type Story = StoryObj<typeof meta>

/** Success — emerald tick. */
export const Success: Story = { args: { status: 'success' } }

/** Failed — rose cross. */
export const Failed: Story = { args: { status: 'failed' } }

/** Dense — glyph only, for tight table cells. */
export const Dense: Story = { args: { status: 'failed', dense: true } }
