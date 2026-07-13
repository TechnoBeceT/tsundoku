import type { Meta, StoryObj } from '@storybook/vue3'
import { ref } from 'vue'
import ScoreSelector from './ScoreSelector.vue'

/**
 * Stories for ScoreSelector — one score input per tracker scale. Each story wires
 * a local ref so clicking a star/face/number or dragging the slider updates the
 * live value. `format` drives which shape renders; `modelValue` seeds the score.
 */
const meta = {
  title: 'UI/ScoreSelector',
  component: ScoreSelector,
  argTypes: {
    format: {
      control: { type: 'inline-radio' },
      options: ['point100', 'point10', 'point10decimal', 'point5', 'point3'],
    },
    modelValue: { control: { type: 'number' } },
  },
  args: { modelValue: 7, format: 'point10' },
} satisfies Meta<typeof ScoreSelector>

export default meta
type Story = StoryObj<typeof meta>

const stateful = (args: Record<string, unknown>) => ({
  components: { ScoreSelector },
  setup: () => {
    const value = ref(args.modelValue as number)
    return { args, value }
  },
  template: '<ScoreSelector v-bind="args" v-model="value" />',
})

/** Ten number buttons (the default AniList/MAL point-10 scale). */
export const Point10: Story = {
  render: stateful,
}

/** Five stars. */
export const Point5: Story = {
  args: { format: 'point5', modelValue: 4 },
  render: stateful,
}

/** Three faces (Kitsu-style simple scale). */
export const Point3: Story = {
  args: { format: 'point3', modelValue: 2 },
  render: stateful,
}

/** A 0–10 slider in half steps. */
export const Point10Decimal: Story = {
  args: { format: 'point10decimal', modelValue: 7.5 },
  render: stateful,
}

/** A 0–100 slider. */
export const Point100: Story = {
  args: { format: 'point100', modelValue: 82 },
  render: stateful,
}

/** Unscored (0) — stars/faces/numbers empty, readout shows an em-dash. */
export const Unscored: Story = {
  args: { format: 'point5', modelValue: 0 },
  render: stateful,
}

/** Disabled — dimmed, no interaction. */
export const Disabled: Story = {
  args: { format: 'point10', disabled: true },
  render: stateful,
}
