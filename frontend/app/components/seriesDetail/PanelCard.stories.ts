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
  argTypes: { title: { control: 'text' }, maxHeight: { control: 'text' } },
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

/** A plain titled panel over a full-bleed body (unbounded — grows with content). */
export const Default: Story = {}

/**
 * QCAT-265 treatment #1 (GAP-093): a CONTENT-KEYED bounded inner-scroll. Passing
 * `maxHeight="240px"` caps the panel and scrolls the body internally while the
 * PAGE keeps growing — the Series-Detail Chapters/Sources shape (there `580px`).
 * 🔴 The bound is a fixed length, NEVER a viewport unit (`100dvh` is banned).
 */
export const Bounded: Story = {
  args: { title: 'Chapters', maxHeight: '240px' },
  render: (args) => ({
    components: { PanelCard },
    setup: () => ({ args, rows: Array.from({ length: 40 }, (_, i) => 40 - i) }),
    template:
      '<PanelCard v-bind="args">'
      + '<template #actions><span class="pill">40</span></template>'
      + '<div v-for="n in rows" :key="n" style="padding:12px 18px;border-bottom:1px solid var(--border);color:var(--muted);font-size:var(--text-base)">Chapter {{ n }}</div>'
      + '</PanelCard>'
      + '<style>.pill{padding:1px 8px;border-radius:var(--radius-pill);background:var(--surface3);color:var(--muted);font-size:var(--text-xs);font-weight:var(--weight-extrabold)}</style>',
  }),
}

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
