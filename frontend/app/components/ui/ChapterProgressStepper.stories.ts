import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import ChapterProgressStepper from './ChapterProgressStepper.vue'

/**
 * Stories for ChapterProgressStepper — the numeric −/＋ spinner. Each story wires
 * a local ref so the buttons actually move the value and the end-clamp disabling
 * is visible. Args drive `modelValue`/`min`/`max`/`disabled`.
 */
const meta = {
  title: 'UI/ChapterProgressStepper',
  component: ChapterProgressStepper,
  argTypes: {
    modelValue: { control: { type: 'number' } },
    min: { control: { type: 'number' } },
    max: { control: { type: 'number' } },
  },
  args: { modelValue: 12, min: 0, max: 180 },
} satisfies Meta<typeof ChapterProgressStepper>

export default meta
type Story = StoryObj<typeof meta>

const stateful = (args: Record<string, unknown>) => ({
  components: { ChapterProgressStepper },
  setup: () => {
    const value = ref(args.modelValue as number)
    return { args, value }
  },
  template: '<ChapterProgressStepper v-bind="args" v-model="value" />',
})

/** Bounded 0–180, mid-range. */
export const Default: Story = {
  render: stateful,
}

/** At the lower bound — the minus button is disabled. */
export const AtMin: Story = {
  args: { modelValue: 0 },
  render: stateful,
}

/** At the upper bound — the plus button is disabled. */
export const AtMax: Story = {
  args: { modelValue: 180 },
  render: stateful,
}

/** No `max` — the value has no upper cap and shows no "/max" suffix. */
export const Unbounded: Story = {
  args: { max: undefined },
  render: stateful,
}

/** Disabled — both buttons blocked. */
export const Disabled: Story = {
  args: { disabled: true },
  render: stateful,
}
