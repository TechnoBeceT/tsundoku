import type { Meta, StoryObj } from '@storybook/vue3'
import Spinner from './Spinner.vue'

/**
 * Stories for the busy-ring Spinner. Exercise the four tones and a size ladder;
 * flip the Storybook theme toolbar to confirm the token-backed tones re-tint.
 * The `on-accent` tone is shown on an accent tile (its intended placement).
 */
const meta = {
  title: 'UI/Spinner',
  component: Spinner,
  argTypes: {
    size: { control: { type: 'range', min: 10, max: 64, step: 2 } },
    tone: { control: { type: 'inline-radio' }, options: ['accent', 'current', 'on-accent', 'dark'] },
  },
  args: { size: 16, tone: 'current' },
} satisfies Meta<typeof Spinner>

export default meta
type Story = StoryObj<typeof meta>

/** Default: inherits `currentColor` (here tinted via the text token). */
export const Default: Story = {
  render: (args) => ({
    components: { Spinner },
    setup: () => ({ args }),
    template: '<div style="color: var(--text)"><Spinner v-bind="args" /></div>',
  }),
}

/** The brand-accent ring. */
export const Accent: Story = {
  args: { tone: 'accent', size: 22 },
}

/** White-ish ring on a solid accent button/tile. */
export const OnAccent: Story = {
  args: { tone: 'on-accent', size: 22 },
  render: (args) => ({
    components: { Spinner },
    setup: () => ({ args }),
    template:
      '<div style="display:inline-flex;padding:14px;border-radius:var(--radius-md);background:var(--accent)"><Spinner v-bind="args" /></div>',
  }),
}

/** All four tones side by side for a quick visual diff. */
export const Tones: Story = {
  render: () => ({
    components: { Spinner },
    template:
      '<div style="display:flex;align-items:center;gap:24px;color:var(--text)">' +
      '<Spinner :size="22" tone="current" />' +
      '<Spinner :size="22" tone="accent" />' +
      '<div style="display:inline-flex;padding:12px;border-radius:var(--radius-md);background:var(--accent)"><Spinner :size="22" tone="on-accent" /></div>' +
      '<div style="display:inline-flex;padding:12px;border-radius:var(--radius-md);background:var(--surface2)"><Spinner :size="22" tone="dark" /></div>' +
      '</div>',
  }),
}

/** Size ladder of the accent ring. */
export const Sizes: Story = {
  render: () => ({
    components: { Spinner },
    template:
      '<div style="display:flex;align-items:center;gap:20px">' +
      '<Spinner :size="14" tone="accent" /><Spinner :size="20" tone="accent" /><Spinner :size="28" tone="accent" /><Spinner :size="40" tone="accent" /><Spinner :size="56" tone="accent" />' +
      '</div>',
  }),
}
