import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import DurationInput from './DurationInput.vue'
import type { DurationValue } from './forms.types'

/**
 * Stories for the DurationInput (number + unit pair). Covers a live-bound default
 * and the disabled state. The number clamps to a non-negative integer on edit; the
 * unit dropdown shows the short h / m / s labels (matching the prototype). The
 * `Seconds` story opens on the `s` unit so that short label is visible at a glance.
 */
const meta = {
  title: 'UI/DurationInput',
  component: DurationInput,
  argTypes: {
    disabled: { control: 'boolean' },
  },
  args: { disabled: false },
  render: (args) => ({
    components: { DurationInput },
    setup: () => {
      const value = ref<DurationValue>({ value: 2, unit: 'h' })
      return { args, value }
    },
    template: '<DurationInput v-bind="args" v-model="value" />',
  }),
} satisfies Meta<typeof DurationInput>

export default meta
type Story = StoryObj<typeof meta>

/** Default — 2 h. */
export const Default: Story = {}

/** Opened on the `s` unit, showing the short seconds label. */
export const Seconds: Story = {
  render: (args) => ({
    components: { DurationInput },
    setup: () => {
      const value = ref<DurationValue>({ value: 30, unit: 's' })
      return { args, value }
    },
    template: '<DurationInput v-bind="args" v-model="value" />',
  }),
}

/** Disabled state. */
export const Disabled: Story = {
  args: { disabled: true },
}
