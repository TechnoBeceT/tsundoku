import type { Meta, StoryObj } from '@storybook/vue3'
import BrandMark from './BrandMark.vue'

/**
 * Stories for the Tsundoku product mark. Exercise the three tones and a range
 * of sizes; flip the Storybook theme toolbar to confirm the gradient/mono marks
 * re-tint with the theme tokens. The `inverse` tone is shown on an accent tile
 * (its intended placement).
 */
const meta = {
  title: 'Brand/BrandMark',
  component: BrandMark,
  argTypes: {
    size: { control: { type: 'range', min: 16, max: 160, step: 4 } },
    tone: { control: { type: 'inline-radio' }, options: ['gradient', 'mono', 'inverse'] },
  },
  args: { size: 96, tone: 'gradient' },
} satisfies Meta<typeof BrandMark>

export default meta
type Story = StoryObj<typeof meta>

/** The primary gradient mark. */
export const Gradient: Story = {
  args: { tone: 'gradient' },
}

/** Single-colour silhouette in `currentColor` (here tinted via inline style). */
export const Mono: Story = {
  args: { tone: 'mono' },
  render: (args) => ({
    components: { BrandMark },
    setup: () => ({ args }),
    template: '<div style="color: var(--text)"><BrandMark v-bind="args" /></div>',
  }),
}

/** White mark on an accent tile — the favicon/app-icon treatment. */
export const Inverse: Story = {
  args: { tone: 'inverse', size: 88 },
  render: (args) => ({
    components: { BrandMark },
    setup: () => ({ args }),
    template:
      '<div style="display:inline-flex;padding:28px;border-radius:var(--radius-2xl);background:linear-gradient(135deg, var(--accent), var(--accentDeep))"><BrandMark v-bind="args" /></div>',
  }),
}

/** Size ladder of the primary mark (nav glyph → hero). */
export const Sizes: Story = {
  render: () => ({
    components: { BrandMark },
    template:
      '<div style="display:flex;align-items:flex-end;gap:20px">' +
      '<BrandMark :size="24" /><BrandMark :size="40" /><BrandMark :size="64" /><BrandMark :size="96" /><BrandMark :size="128" />' +
      '</div>',
  }),
}

/** All three tones side by side for a quick visual diff. */
export const Tones: Story = {
  render: () => ({
    components: { BrandMark },
    template:
      '<div style="display:flex;align-items:center;gap:24px;color:var(--text)">' +
      '<BrandMark :size="72" tone="gradient" />' +
      '<BrandMark :size="72" tone="mono" />' +
      '<div style="display:inline-flex;padding:18px;border-radius:var(--radius-xl);background:linear-gradient(135deg,var(--accent),var(--accentDeep))"><BrandMark :size="72" tone="inverse" /></div>' +
      '</div>',
  }),
}
