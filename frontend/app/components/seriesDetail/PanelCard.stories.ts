import type { Meta, StoryObj } from '@storybook/vue3'
import PanelCard from './PanelCard.vue'
import AppButton from '../ui/AppButton.vue'

/**
 * Stories for PanelCard — the shared Series-Detail divided-panel shell. Covers a
 * plain titled panel, a header-right `actions` count pill (the Chapters card
 * shape), and a header-left `lead` pill grouped with the title plus a header-right
 * add button (the Sources card shape). Each sits in a card-width frame; flip the
 * theme toolbar to confirm the `--surface` panel + `--border` rule re-tint in both
 * modes.
 */
const meta = {
  title: 'SeriesDetail/PanelCard',
  component: PanelCard,
  parameters: { layout: 'padded' },
  argTypes: { title: { control: 'text' } },
  args: { title: 'Chapters' },
  decorators: [
    () => ({ template: '<div style="width:420px"><story /></div>' }),
  ],
  render: (args) => ({
    components: { PanelCard },
    setup: () => ({ args }),
    template:
      '<PanelCard v-bind="args">'
      + '<div style="padding:16px;color:var(--muted);font-size:13.5px">A full-bleed body — the shell adds no padding of its own.</div>'
      + '</PanelCard>',
  }),
} satisfies Meta<typeof PanelCard>

export default meta
type Story = StoryObj<typeof meta>

/** A plain titled panel over a full-bleed body. */
export const Default: Story = {}

/** Header-right `actions` count pill across from the title (the Chapters shape). */
export const WithCountPill: Story = {
  render: (args) => ({
    components: { PanelCard },
    setup: () => ({ args }),
    template:
      '<PanelCard v-bind="args">'
      + '<template #actions><span class="pill">128</span></template>'
      + '<div style="padding:16px;color:var(--muted);font-size:13.5px">128 chapters listed below.</div>'
      + '</PanelCard>'
      // Inline pill so the story is self-contained (each consumer styles its own).
      + '<style>.pill{padding:1px 8px;border-radius:var(--radius-pill);background:var(--surface3);color:var(--muted);font-size:var(--text-xs);font-weight:var(--weight-extrabold)}</style>',
  }),
}

/** Header-left `lead` pill beside the title + a header-right add button (the Sources shape). */
export const WithLeadAndAction: Story = {
  args: { title: 'Sources' },
  render: (args) => ({
    components: { PanelCard, AppButton },
    setup: () => ({ args }),
    template:
      '<PanelCard v-bind="args">'
      + '<template #lead><span class="pill">3</span></template>'
      + '<template #actions><AppButton variant="mini" size="sm">Add</AppButton></template>'
      + '<div style="padding:16px;color:var(--muted);font-size:13.5px">Three ranked sources, preferred first.</div>'
      + '</PanelCard>'
      + '<style>.pill{padding:1px 8px;border-radius:var(--radius-pill);background:var(--surface3);color:var(--muted);font-size:var(--text-xs);font-weight:var(--weight-extrabold)}</style>',
  }),
}
