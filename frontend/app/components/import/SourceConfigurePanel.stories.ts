import type { Meta, StoryObj } from '@storybook/vue3'
import SourceConfigurePanel from './SourceConfigurePanel.vue'
import type { DisplayRow } from '~/composables/useSourceConfigure'
import type { SearchCandidate } from '../screens/import.types'

/**
 * Stories for the shared Configure-stage rows block. `toggle`, `move`, and
 * `inspect` are logged in the Actions panel. The variants cover: no rows
 * (empty tray), a single selected row, a multi-row ranked selection with
 * coverage, a per-scanlator split source, and the whole-panel `hideInspect`
 * opt-out (the single-select match surfaces). Flip the Storybook theme
 * toolbar to confirm both themes.
 */
const meta = {
  title: 'Import/SourceConfigurePanel',
  component: SourceConfigurePanel,
  parameters: {
    layout: 'padded',
    actions: { handles: ['toggle', 'move', 'inspect'] },
  },
  decorators: [() => ({ template: '<div style="max-width:780px"><story /></div>' })],
  args: {
    rows: [],
  },
} satisfies Meta<typeof SourceConfigurePanel>

export default meta
type Story = StoryObj<typeof meta>

const cover = (id: number): string => `https://picsum.photos/seed/scp-${id}/120/160`

const mangaDex: SearchCandidate = {
  source: '2499283573021220255',
  sourceName: 'MangaDex',
  lang: 'en',
  mangaId: 1001,
  url: '/manga/1001/solo-leveling',
  title: 'Solo Leveling',
  thumbnailUrl: cover(1001),
}

const asuraScans: SearchCandidate = {
  source: '1024627298672457456',
  sourceName: 'Asura Scans',
  lang: 'en',
  mangaId: 1002,
  url: '/manga/1002/solo-leveling',
  title: 'Solo Leveling',
  thumbnailUrl: cover(1002),
}

const manganato: SearchCandidate = {
  source: '3437691801785968169',
  sourceName: 'Manganato',
  lang: 'en',
  mangaId: 1003,
  url: '/manga/1003/solo-leveling',
  title: 'Solo Leveling',
  thumbnailUrl: '',
}

const comix: SearchCandidate = {
  source: '5183633796946525193',
  sourceName: 'Comix',
  lang: 'en',
  mangaId: 2001,
  url: '/manga/2001/omniscient-reader',
  title: 'Omniscient Reader',
  thumbnailUrl: cover(2001),
}

/** No rows — the tray/group is empty (nothing selected yet). */
export const Empty: Story = {
  args: {
    rows: [],
  },
}

/** One selected row, no coverage resolved yet — the plain unsplit shape. */
export const Single: Story = {
  args: {
    rows: [
      {
        key: 'mangadex:1001',
        candidate: mangaDex,
        scanlator: '',
        scanlatorParam: '',
        chapterCount: undefined,
        chapterRanges: '',
        coverageUnavailable: false,
        isSplit: false,
        selected: true,
        rank: 1,
        canUp: false,
        canDown: false,
      } satisfies DisplayRow,
    ],
  },
}

/** Three selected rows, ranked 1/2/3, each with resolved coverage. */
export const MultiRanked: Story = {
  args: {
    rows: [
      {
        key: 'mangadex:1001',
        candidate: mangaDex,
        scanlator: '',
        scanlatorParam: '',
        chapterCount: 180,
        chapterRanges: '1-180',
        coverageUnavailable: false,
        isSplit: false,
        selected: true,
        rank: 1,
        canUp: false,
        canDown: true,
      },
      {
        key: 'asurascans:1002',
        candidate: asuraScans,
        scanlator: '',
        scanlatorParam: '',
        chapterCount: 175,
        chapterRanges: '1-175',
        coverageUnavailable: false,
        isSplit: false,
        selected: true,
        rank: 2,
        canUp: true,
        canDown: true,
      },
      {
        key: 'manganato:1003',
        candidate: manganato,
        scanlator: '',
        scanlatorParam: '',
        chapterCount: 90,
        chapterRanges: '1-90',
        coverageUnavailable: false,
        isSplit: false,
        selected: true,
        rank: 3,
        canUp: true,
        canDown: false,
      },
    ] satisfies DisplayRow[],
  },
}

/**
 * A single source auto-split into two per-scanlator rows (2+ groups) — each
 * row's Inspect button is hidden (`isSplit`; coverage is already inline).
 */
export const SplitScanlator: Story = {
  args: {
    rows: [
      {
        key: 'comix:2001:Reset Scans',
        candidate: comix,
        scanlator: 'Reset Scans',
        scanlatorParam: 'Reset Scans',
        chapterCount: 42,
        chapterRanges: '1-42',
        coverageUnavailable: false,
        isSplit: true,
        selected: true,
        rank: 1,
        canUp: false,
        canDown: true,
      },
      {
        key: 'comix:2001:ZScans',
        candidate: comix,
        scanlator: 'ZScans',
        scanlatorParam: 'ZScans',
        chapterCount: 58,
        chapterRanges: '43-90, 92-101',
        coverageUnavailable: false,
        isSplit: true,
        selected: true,
        rank: 2,
        canUp: true,
        canDown: false,
      },
    ] satisfies DisplayRow[],
  },
}

/**
 * `hideInspect: true` — the single-select match surfaces (`scanLibrary/
 * MatchPanel`, `seriesDetail/MatchSourceDialog`) reuse this panel with no live
 * chapter-inspect endpoint; every row's Inspect button disappears regardless
 * of `isSplit`.
 */
export const InspectHidden: Story = {
  args: {
    hideInspect: true,
    rows: [
      {
        key: 'mangadex:1001',
        candidate: mangaDex,
        scanlator: '',
        scanlatorParam: '',
        chapterCount: 180,
        chapterRanges: '1-180',
        coverageUnavailable: false,
        isSplit: false,
        selected: true,
        rank: 1,
        canUp: false,
        canDown: false,
      } satisfies DisplayRow,
    ],
  },
}
