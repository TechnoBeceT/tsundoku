import type { Meta, StoryObj } from '@storybook/vue3'
import ReadMore from './ReadMore.vue'

/**
 * Stories for ReadMore. The toggle appears ONLY when the text overflows the
 * clamp, so the two key states are a long synopsis (toggle shown, expands in
 * place) and a short one (no toggle). Constrained widths in the render force the
 * overflow deterministically. Flip the theme toolbar to check both themes.
 */
const longText =
  'Ten years ago, after "the Gate" that connected the real world with the ' +
  'monster world opened, some of the ordinary, everyday people received the ' +
  'power to hunt monsters within the Gate. They are known as "Hunters". However, ' +
  'not all Hunters are powerful. My name is Sung Jin-Woo, an E-rank Hunter. I\'m ' +
  'someone who has to risk his life in the lowliest of dungeons, the "World\'s ' +
  'Weakest". Having no skills whatsoever to display, I barely earned the required ' +
  'money by fighting in low-leveled dungeons… at least until I found a hidden ' +
  'dungeon with the hardest difficulty within the D-rank dungeons!'

const meta = {
  title: 'UI/ReadMore',
  component: ReadMore,
  argTypes: {
    lines: { control: { type: 'number' } },
  },
  args: { text: longText, lines: 4 },
} satisfies Meta<typeof ReadMore>

export default meta
type Story = StoryObj<typeof meta>

/** Long text overflowing a 4-line clamp — the toggle is shown. */
export const Overflowing: Story = {
  render: (args) => ({
    components: { ReadMore },
    setup: () => ({ args }),
    template: '<div style="max-width:420px"><ReadMore v-bind="args" /></div>',
  }),
}

/** Short text that fits — no toggle is rendered. */
export const ShortNoToggle: Story = {
  args: { text: 'A hunter levels up in a world of monster gates.' },
  render: (args) => ({
    components: { ReadMore },
    setup: () => ({ args }),
    template: '<div style="max-width:420px"><ReadMore v-bind="args" /></div>',
  }),
}

/** A tighter two-line clamp — smaller preview before "Read more". */
export const TwoLineClamp: Story = {
  args: { lines: 2 },
  render: (args) => ({
    components: { ReadMore },
    setup: () => ({ args }),
    template: '<div style="max-width:420px"><ReadMore v-bind="args" /></div>',
  }),
}
