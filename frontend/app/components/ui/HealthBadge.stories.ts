import type { Meta, StoryObj } from '@storybook/vue3'
import HealthBadge from './HealthBadge.vue'

/**
 * Stories for the provider-health HealthBadge. The `All` story shows every
 * health state; flip the Storybook theme toolbar to confirm the palette reads
 * on both surfaces.
 */
const meta = {
  title: 'UI/HealthBadge',
  component: HealthBadge,
  argTypes: {
    health: { control: { type: 'inline-radio' }, options: ['ok', 'stale', 'erroring', 'unavailable'] },
  },
  args: { health: 'ok' },
} satisfies Meta<typeof HealthBadge>

export default meta
type Story = StoryObj<typeof meta>

/** A single badge driven by the `health` control. */
export const Playground: Story = {}

/** Every provider-health state, including the extension-gone `unavailable`. */
export const All: Story = {
  render: () => ({
    components: { HealthBadge },
    setup: () => ({ healths: ['ok', 'stale', 'erroring', 'unavailable'] as const }),
    template:
      '<div style="display:flex;flex-wrap:wrap;gap:10px">' +
      '<HealthBadge v-for="h in healths" :key="h" :health="h" />' +
      '</div>',
  }),
}

/** The extension-uninstalled state on its own — the new distinct slate badge. */
export const Unavailable: Story = {
  args: { health: 'unavailable' },
}
