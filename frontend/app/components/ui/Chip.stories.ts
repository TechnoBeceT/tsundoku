import type { Meta, StoryObj } from '@storybook/vue3'
import Chip from './Chip.vue'

/**
 * Stories for the Chip pill. Each variant is shown with sample text; the `frost`
 * variant is placed on a dark cover tile (its intended on-cover home), and the
 * Icon story exercises the leading `icon` slot. Flip the Storybook theme toolbar
 * to confirm every variant re-tints from the tokens.
 */
const meta = {
  title: 'UI/Chip',
  component: Chip,
  argTypes: {
    variant: {
      control: { type: 'inline-radio' },
      options: ['neutral', 'category', 'language', 'accent', 'frost'],
    },
    size: { control: { type: 'inline-radio' }, options: ['md', 'sm'] },
  },
  args: { variant: 'neutral', size: 'md', default: 'Manga' },
} satisfies Meta<typeof Chip>

export default meta
type Story = StoryObj<typeof meta>

/** The default house-neutral pill (same look as `category`). */
export const Neutral: Story = {
  render: (args) => ({
    components: { Chip },
    setup: () => ({ args }),
    template: '<Chip v-bind="args">Manga</Chip>',
  }),
}

/** Category badge treatment (identical to neutral — the library category pill). */
export const Category: Story = {
  args: { variant: 'category' },
  render: (args) => ({
    components: { Chip },
    setup: () => ({ args }),
    template: '<Chip v-bind="args">Manhwa</Chip>',
  }),
}

/** Mono short-code pill (language / scanlator codes). */
export const Language: Story = {
  args: { variant: 'language' },
  render: (args) => ({
    components: { Chip },
    setup: () => ({ args }),
    template: '<Chip v-bind="args">EN</Chip>',
  }),
}

/** Accent-tinted highlight pill. */
export const Accent: Story = {
  args: { variant: 'accent' },
  render: (args) => ({
    components: { Chip },
    setup: () => ({ args }),
    template: '<Chip v-bind="args">12 wanted</Chip>',
  }),
}

/** Frost pill shown on a dark cover tile (its intended placement). */
export const Frost: Story = {
  args: { variant: 'frost' },
  render: (args) => ({
    components: { Chip },
    setup: () => ({ args }),
    template:
      '<div style="display:inline-flex;padding:24px;border-radius:var(--radius-xl);background:var(--cover-placeholder)"><Chip v-bind="args">Manga</Chip></div>',
  }),
}

/** With a leading icon via the `icon` slot. */
export const WithIcon: Story = {
  args: { variant: 'accent' },
  render: (args) => ({
    components: { Chip },
    setup: () => ({ args }),
    template:
      '<Chip v-bind="args"><template #icon><svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M20 6L9 17l-5-5" /></svg></template>Preferred</Chip>',
  }),
}

/** The md (default) and sm font steps — sm is the shared on-tile step (badge.css). */
export const Sizes: Story = {
  render: () => ({
    components: { Chip },
    template:
      '<div style="display:flex;align-items:center;gap:10px">' +
      '<Chip variant="category" size="md">Manhwa</Chip>' +
      '<Chip variant="category" size="sm">Manhwa</Chip>' +
      '</div>',
  }),
}

/** All variants side by side for a quick visual diff. */
export const AllVariants: Story = {
  render: () => ({
    components: { Chip },
    template:
      '<div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap">' +
      '<Chip variant="neutral">Neutral</Chip>' +
      '<Chip variant="category">Manga</Chip>' +
      '<Chip variant="language">EN</Chip>' +
      '<Chip variant="accent">Accent</Chip>' +
      '<div style="display:inline-flex;padding:10px;border-radius:var(--radius-lg);background:var(--cover-placeholder)"><Chip variant="frost">Frost</Chip></div>' +
      '</div>',
  }),
}
