import type { Meta, StoryObj } from '@storybook/vue3'
import BrandLockup from './BrandLockup.vue'

/**
 * Stories for the full Tsundoku lockup. Covers the primary lockup, the
 * wordmark-only variant (nav rail), and sizing. Flip the theme toolbar to
 * confirm the wordmark + subtitle re-tint with the tokens.
 */
const meta = {
  title: 'Brand/BrandLockup',
  component: BrandLockup,
  argTypes: {
    size: { control: { type: 'range', min: 24, max: 96, step: 2 } },
    tone: { control: { type: 'inline-radio' }, options: ['gradient', 'mono', 'inverse'] },
    subtitle: { control: 'boolean' },
    japanese: { control: 'boolean' },
  },
  args: { size: 44, tone: 'gradient', subtitle: true, japanese: true },
} satisfies Meta<typeof BrandLockup>

export default meta
type Story = StoryObj<typeof meta>

/** The full primary lockup — mark + wordmark + 積ん読 tagline row. */
export const Primary: Story = {}

/** Wordmark + mark only, no subtitle (compact header / nav rail use). */
export const WordmarkOnly: Story = {
  args: { subtitle: false },
}

/** Tagline without the Japanese glyph. */
export const NoJapanese: Story = {
  args: { japanese: false },
}

/** Larger hero lockup. */
export const Hero: Story = {
  args: { size: 72 },
}
