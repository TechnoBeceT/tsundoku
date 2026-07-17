import type { Meta, StoryObj } from '@storybook/vue3'
import DisclosurePanel from './DisclosurePanel.vue'

/**
 * Stories for the shared DisclosurePanel — the app's one collapse/expand panel
 * shell (QCAT-265 treatment #2). `Static` is the non-collapsible titled shell;
 * `Collapsible` / `CollapsedByDefault` show the trigger driving a long list —
 * click the heading and watch the body take or give back its space, which is the
 * whole point: the panel GROWS with its content, it never letterboxes it into a
 * nested scroller. `WithActions` proves header buttons stay independently
 * clickable beside the trigger, and `Flat` shows the chrome-less variant used
 * inside an existing card. Flip the theme toolbar — every colour comes from the
 * tokens.
 */
const rows = (n: number): string =>
  Array.from({ length: n }, (_, i) => `<div class="row">Chapter ${n - i}</div>`).join('')

const ROW_CSS = `
  .row { padding: var(--space-md) var(--space-2xl-tight); border-bottom: 1px solid var(--border); color: var(--muted); font-size: var(--text-base); }
  .row:last-child { border-bottom: none; }
`

const meta = {
  title: 'UI/DisclosurePanel',
  component: DisclosurePanel,
  args: {
    title: 'Chapters',
    count: 12,
    collapsible: true,
    defaultOpen: true,
    flat: false,
  },
  render: (args) => ({
    components: { DisclosurePanel },
    setup: () => ({ args }),
    template: `
      <div style="max-width:560px">
        <style>${ROW_CSS}</style>
        <DisclosurePanel v-bind="args">${rows(12)}</DisclosurePanel>
      </div>`,
  }),
} satisfies Meta<typeof DisclosurePanel>

export default meta
type Story = StoryObj<typeof meta>

/** The plain always-open titled panel — no trigger, no chevron. */
export const Static: Story = {
  args: { collapsible: false },
}

/** Collapsible and open — click the heading to give the space back. */
export const Collapsible: Story = {}

/** Collapsed on arrival: the header alone, holding a long list in reserve. */
export const CollapsedByDefault: Story = {
  args: { defaultOpen: false },
}

/** A summary keeps context visible while the body is hidden. */
export const WithSummary: Story = {
  args: { defaultOpen: false, count: null, summary: '3 of 40 selected' },
}

/** Header actions sit OUTSIDE the trigger, so they stay independently clickable. */
export const WithActions: Story = {
  render: (args) => ({
    components: { DisclosurePanel },
    setup: () => ({ args }),
    template: `
      <div style="max-width:560px">
        <style>${ROW_CSS}</style>
        <DisclosurePanel v-bind="args">
          <template #actions>
            <button style="padding:6px 12px;border-radius:999px;border:1px solid var(--border);background:var(--surface2);color:var(--muted);font-size:12px;cursor:pointer">Add</button>
          </template>
          ${rows(6)}
        </DisclosurePanel>
      </div>`,
  }),
}

/** A long list — the panel grows to fit it; the page (not a nested band) scrolls. */
export const LongList: Story = {
  args: { count: 60 },
  render: (args) => ({
    components: { DisclosurePanel },
    setup: () => ({ args }),
    template: `
      <div style="max-width:560px">
        <style>${ROW_CSS}</style>
        <DisclosurePanel v-bind="args">${rows(60)}</DisclosurePanel>
      </div>`,
  }),
}

/** The chrome-less variant — for a disclosure nested inside an existing card. */
export const Flat: Story = {
  args: { flat: true, title: 'Limit to:', count: null, summary: '2 selected' },
  render: (args) => ({
    components: { DisclosurePanel },
    setup: () => ({ args }),
    template: `
      <div style="max-width:560px;padding:18px;border:1px solid var(--border);border-radius:18px;background:var(--surface)">
        <DisclosurePanel v-bind="args">
          <div style="display:flex;gap:7px;flex-wrap:wrap">
            <span v-for="s in ['MangaDex','Asura Scans','Comix','KaliScan']" :key="s"
                  style="padding:6px 12px;border-radius:999px;border:1px solid var(--border);background:var(--surface2);color:var(--muted);font-size:13px">{{ s }}</span>
          </div>
        </DisclosurePanel>
      </div>`,
  }),
}
