import type { Meta, StoryObj } from '@storybook/vue3'
import ProgressBar from './ProgressBar.vue'

/**
 * Stories for ProgressBar — determinate (a fixed fill) and indeterminate (the
 * sliding bar for unknown-duration jobs). Bars are width-constrained in a frame
 * so the track is visible.
 */
const meta = {
  title: 'UI/ProgressBar',
  component: ProgressBar,
  argTypes: {
    value: { control: { type: 'range', min: 0, max: 100, step: 1 } },
  },
  args: { value: 60 },
  decorators: [
    () => ({ template: '<div style="width:280px"><story /></div>' }),
  ],
} satisfies Meta<typeof ProgressBar>

export default meta
type Story = StoryObj<typeof meta>

/** Determinate fill driven by the `value` control. */
export const Determinate: Story = {
  args: { value: 60 },
}

/** No `value` → the indeterminate sliding bar. */
export const Indeterminate: Story = {
  args: { value: undefined },
}

/** A ladder of determinate fills. */
export const Steps: Story = {
  render: () => ({
    components: { ProgressBar },
    template:
      '<div style="display:flex;flex-direction:column;gap:14px;width:280px">' +
      '<ProgressBar :value="10" /><ProgressBar :value="45" /><ProgressBar :value="80" /><ProgressBar :value="100" />' +
      '</div>',
  }),
}

/** Custom track + tone tokens. */
export const Tinted: Story = {
  args: { value: 70, track: 'var(--surface2)', tone: 'var(--accentBright)' },
}
