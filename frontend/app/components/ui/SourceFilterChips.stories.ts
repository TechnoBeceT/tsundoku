import type { Meta, StoryObj } from '@storybook/vue3'
import SourceFilterChips from './SourceFilterChips.vue'

/**
 * Stories for the SourceFilterChips filter row. `Empty` shows the resting state
 * (nothing selected), `SomeSelected` / `AllSelected` show the accent-tinted
 * active pills, and `ManySources` demonstrates the row wrapping across many
 * sources. Every story wires `selected` live via the `update:selected` event so
 * clicking a chip toggles it in the Storybook canvas. Flip the theme toolbar to
 * confirm the active pills re-tint from the tokens in both themes.
 */
const sources = [
  { id: '1', name: 'MangaDex' },
  { id: '2', name: 'Asura Scans' },
  { id: '3', name: 'Manganato' },
  { id: '4', name: 'Weeb Central' },
]

const manySources = [
  { id: '1', name: 'MangaDex' },
  { id: '2', name: 'Asura Scans' },
  { id: '3', name: 'Manganato' },
  { id: '4', name: 'Weeb Central' },
  { id: '5', name: 'Comix' },
  { id: '6', name: 'KaliScan' },
  { id: '7', name: 'Reaper Scans' },
  { id: '8', name: 'FlameComics' },
  { id: '9', name: 'Bato.to' },
  { id: '10', name: 'MangaKakalot' },
]

const meta = {
  title: 'UI/SourceFilterChips',
  component: SourceFilterChips,
  args: { sources, selected: [] },
  render: (args) => ({
    components: { SourceFilterChips },
    setup: () => ({ args }),
    template: '<SourceFilterChips v-bind="args" @update:selected="args.selected = $event" />',
  }),
} satisfies Meta<typeof SourceFilterChips>

export default meta
type Story = StoryObj<typeof meta>

/** Nothing selected — every chip is quiet. */
export const Empty: Story = {
  args: { selected: [] },
}

/** A couple of sources selected — accent-tinted active pills. */
export const SomeSelected: Story = {
  args: { selected: ['1', '3'] },
}

/** Every source selected. */
export const AllSelected: Story = {
  args: { selected: sources.map(s => s.id) },
}

/** Many sources — the row wraps onto multiple lines. */
export const ManySources: Story = {
  args: { sources: manySources, selected: ['2', '5'] },
}

/**
 * Degraded sources — two sources whose anti-ban circuit-breaker is cooling down
 * are dimmed and carry a ⚠ marker (reason on hover), while the rest read
 * normally. They stay SELECTABLE: a degraded chip is a hint, never a hard block.
 */
export const Degraded: Story = {
  args: {
    sources: [
      { id: '1', name: 'MangaDex' },
      { id: '2', name: 'Asura Scans', degraded: true, degradedReason: 'Temporarily unavailable — 4 consecutive failures' },
      { id: '3', name: 'Manganato' },
      { id: '4', name: 'Comick', degraded: true, degradedReason: 'Temporarily unavailable — 3 consecutive failures' },
    ],
    selected: ['1'],
  },
}
