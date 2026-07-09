/**
 * useSourceConfigure — the Adopt wizard's Configure-stage orchestration
 * (multi-select / per-scanlator coverage split / rank), extracted from
 * `Import.vue` (Slice P) so it can be shared by Import.vue + the two
 * Series-Detail Match dialogs. Pure composable: no fetching, no Pinia — the
 * consumer owns data (the `breakdowns` cache) and side effects
 * (`onLoadBreakdowns`).
 *
 * Ownership split with the consumer: `title`/`category`/inspect state (which
 * candidate's chapter list is being previewed) STAY in the consumer — they're
 * Adopt-only concerns. This composable owns only the tray (cross-search
 * candidate accumulation), the picked `group`, the row selection/order, and
 * the derived per-scanlator rows + ranked provider list.
 */
import { computed, ref, watch, type ComputedRef, type Ref } from 'vue'
import {
  candKey,
  type ScanlatorCoverage,
  type SearchCandidate,
  type SearchGroup,
} from '~/components/screens/import.types'
import { collapseUntaggedScanlator } from '~/utils/scanlator'

/** One Configure-stage row: either a whole source (unsplit) or one of its scanlators (split). */
export interface ConfigRow {
  key: string
  candidate: SearchCandidate
  /** Scanlator subtitle to show under the source name; "" hides it (untagged/unsplit/unavailable). */
  scanlator: string
  /** The value to send as this row's `AdoptProvider.scanlator` ("" = all chapters from the source). */
  scanlatorParam: string
  /** Chapter count for this row's coverage, when the breakdown is available. */
  chapterCount?: number
  /** Human-readable chapter-range string, e.g. "1-90, 92-101". */
  chapterRanges: string
  /** True when this source's breakdown fetch failed (no split, no coverage — "Coverage unavailable"). */
  coverageUnavailable: boolean
  /** True for a per-scanlator split row (2+ scanlators) — hides the Inspect button (coverage is already inline). */
  isSplit: boolean
}

/** `ConfigRow` merged with this row's current selection + rank state (no inspect fields — panel-owned). */
export interface DisplayRow extends ConfigRow {
  selected: boolean
  rank: number
  canUp: boolean
  canDown: boolean
}

/** One resolved provider to adopt, in best-first order. */
export interface ProviderRef {
  source: string
  mangaId: number
  scanlator: string
}

export function useSourceConfigure(opts: {
  /** Per-scanlator breakdown cache, keyed by `source:mangaId` (owned by the consumer). */
  breakdowns: Ref<Record<string, ScanlatorCoverage[] | null>>
  /** Requests a breakdown fetch for each of the given candidates. */
  onLoadBreakdowns: (candidates: SearchCandidate[]) => void
}): {
  tray: Ref<SearchCandidate[]>
  trayActive: ComputedRef<boolean>
  isGroupAdded: (g: SearchGroup) => boolean
  addGroup: (g: SearchGroup) => void
  removeGroup: (g: SearchGroup) => void
  removeCand: (key: string) => void
  suggestedTrayTitle: ComputedRef<string | undefined>
  configureTray: () => void
  group: Ref<SearchGroup | null>
  enterConfigure: (candidates: SearchCandidate[]) => void
  displayRows: ComputedRef<DisplayRow[]>
  orderedKeys: ComputedRef<string[]>
  selectedCount: ComputedRef<number>
  toggleCand: (key: string) => void
  moveCand: (key: string, dir: -1 | 1) => void
  orderedProviders: ComputedRef<ProviderRef[]>
} {
  const group = ref<SearchGroup | null>(null)
  // row key → selected?; `order` holds the selected keys in priority order. A row
  // key is `source:mangaId` (unsplit) or `source:mangaId:scanlator` (once a
  // candidate's breakdown resolves with 2+ scanlators — see the `breakdowns`
  // watch below, which migrates the key(s) in place).
  const selected = ref<Record<string, boolean>>({})
  const order = ref<string[]>([])
  // Candidates (by base `source:mangaId` key) whose breakdown has already been
  // split into per-scanlator row keys — guards the watch below from re-splitting
  // (and duplicating) the same candidate on every unrelated `breakdowns` update.
  const splitApplied = new Set<string>()

  /**
   * Once a candidate's breakdown resolves with 2+ scanlators, migrate its single
   * `source:mangaId` row key to one `source:mangaId:scanlator` key per group —
   * spliced into `order` at the same position (preserving rank) and defaulted to
   * the replaced key's own selected state (mirrors `enterConfigure`'s "select
   * all" default: an unsplit row selected when it split stays fully selected;
   * one deselected before its breakdown resolved stays fully deselected). A 0/1-
   * scanlator or failed/unloaded breakdown never splits — `configRows` below
   * renders those straight off the unsplit key with no reconciliation needed.
   *
   * Watches BOTH `opts.breakdowns` (the normal case: the fetch resolves after
   * `enterConfigure` already ran) AND `group` (the already-cached case: a
   * candidate's breakdown was fetched during an earlier visit to Stage 2 and
   * `opts.breakdowns` already holds 2+ scanlators the moment `enterConfigure`
   * sets `group` — a breakdowns-only watch would never re-fire since the prop
   * itself doesn't change on a re-pick).
   */
  watch([opts.breakdowns, group], () => {
    const g = group.value
    if (!g) return
    for (const c of g.candidates) {
      const baseKey = candKey(c)
      if (splitApplied.has(baseKey)) continue
      const bd = opts.breakdowns.value[baseKey]
      if (!bd || bd.length < 2) continue
      splitApplied.add(baseKey)

      const newKeys = bd.map(sc => `${baseKey}:${sc.scanlator}`)
      const wasSelected = !!selected.value[baseKey]
      const idx = order.value.indexOf(baseKey)
      if (idx >= 0) {
        order.value = [
          ...order.value.slice(0, idx),
          ...(wasSelected ? newKeys : []),
          ...order.value.slice(idx + 1),
        ]
      }
      const { [baseKey]: _removed, ...rest } = selected.value
      const nextSelected = { ...rest }
      for (const k of newKeys) nextSelected[k] = wasSelected
      selected.value = nextSelected
    }
  }, { deep: true })

  /**
   * Seeds Stage 2 from an arbitrary candidate list: all candidates start
   * selected, in list order, and a breakdown fetch is requested for each.
   * Shared by the classic single-group pick AND `configureTray` (the tray's
   * "Configure N sources →"), so Stage 2/3 have exactly one entry point.
   * `title`/`category` stay the consumer's concern — the synthetic `group`'s
   * own `title` is only a display placeholder (the consumer seeds its OWN
   * title ref from whatever it had on hand — the picked group's title, or
   * `suggestedTrayTitle` for the tray path).
   */
  function enterConfigure(candidates: SearchCandidate[]): void {
    const keys = candidates.map(candKey)
    group.value = { title: candidates[0]?.title ?? '', candidates }
    selected.value = Object.fromEntries(keys.map(k => [k, true]))
    order.value = keys
    splitApplied.clear()
    opts.onLoadBreakdowns(candidates)
  }

  // ---- Cross-search adopt tray ------------------------------------------------
  // Accumulates matched groups' candidates ACROSS MULTIPLE searches. Owned here
  // (not derived from search results) so a new search — which replaces that
  // wholesale — never drops what the owner already gathered. Add-unit is a whole
  // group; individual candidates can still be dropped one at a time.
  const tray = ref<SearchCandidate[]>([])
  // The groups that contributed to the tray, kept only to pick a representative
  // title for `suggestedTrayTitle` (dropped again once its whole group is taken
  // back out of the tray).
  const addedGroups = ref<SearchGroup[]>([])

  const trayActive = computed(() => tray.value.length > 0)

  /** True once every candidate of `g` is already in the tray (drives the "✓ Added" state). */
  const isGroupAdded = (g: SearchGroup): boolean =>
    g.candidates.length > 0 && g.candidates.every(c => tray.value.some(t => candKey(t) === candKey(c)))

  /** Add every not-yet-tracked candidate of `g` to the tray, deduped by `candKey`. */
  const addGroup = (g: SearchGroup): void => {
    const existing = new Set(tray.value.map(candKey))
    const toAdd = g.candidates.filter(c => !existing.has(candKey(c)))
    if (toAdd.length === 0) return
    tray.value = [...tray.value, ...toAdd]
    addedGroups.value = [...addedGroups.value, g]
  }

  /** Drop every candidate of `g` from the tray (the "✓ Added" toggle, switched off). */
  const removeGroup = (g: SearchGroup): void => {
    const keys = new Set(g.candidates.map(candKey))
    tray.value = tray.value.filter(c => !keys.has(candKey(c)))
    addedGroups.value = addedGroups.value.filter(x => x.title !== g.title)
  }

  /** Drop one candidate from the tray by its `candKey` (an `AdoptTray` chip remove). */
  const removeCand = (key: string): void => {
    tray.value = tray.value.filter(c => candKey(c) !== key)
  }

  /** The largest contributing group's title — a reasonable default the consumer can seed its own title from. */
  const suggestedTrayTitle = computed<string | undefined>(() => {
    const largest = [...addedGroups.value].sort((a, b) => b.candidates.length - a.candidates.length)[0]
    return largest?.title
  })

  /**
   * "Configure N sources →" — builds a synthetic group from every tray candidate
   * and runs the SAME Stage-2 seeding as a classic pick.
   */
  const configureTray = (): void => {
    if (tray.value.length === 0) return
    enterConfigure([...tray.value])
  }

  // ---- Stage 2: configure ----------------------------------------------------
  // The selected rows, in current priority order (drives rank + importance).
  const orderedKeys = computed(() => order.value.filter(k => selected.value[k]))
  const selectedCount = computed(() => orderedKeys.value.length)

  /**
   * One row per candidate, auto-split into one row per scanlator once that
   * candidate's breakdown resolves with 2+ groups (a 0/1-scanlator or
   * unavailable/unloaded breakdown stays a single row). A single-scanlator
   * breakdown whose one group is named after the source itself (the backend's
   * "untagged" convention) resolves to `scanlator: ''`/no subtitle — it's still
   * an "all chapters" provider, not a named filter; a split row's group keeps
   * its own name verbatim even in the rare case one of several groups happens
   * to share the source's name.
   */
  const configRows = computed<ConfigRow[]>(() => {
    const g = group.value
    if (!g) return []
    const rows: ConfigRow[] = []
    for (const c of g.candidates) {
      const baseKey = candKey(c)
      const bd = opts.breakdowns.value[baseKey]
      if (bd && bd.length >= 2) {
        for (const sc of bd) {
          // The untagged bucket (SourceBreakdown labels it with the source name)
          // must adopt as scanlator "" — the backend's filterByScanlator keeps
          // only chapters whose Chapter.Scanlator EQUALS the param, and untagged
          // chapters carry "", so sending the source name here would match ZERO
          // chapters (a silently-empty, never-downloading provider). The collapse
          // lives in the shared collapseUntaggedScanlator helper; it applies even
          // inside a 2+-group split where one group IS the source-name bucket.
          const param = collapseUntaggedScanlator(sc.scanlator, c.sourceName)
          rows.push({
            key: `${baseKey}:${sc.scanlator}`,
            candidate: c,
            scanlator: param,
            scanlatorParam: param,
            chapterCount: sc.count,
            chapterRanges: sc.ranges,
            coverageUnavailable: false,
            isSplit: true,
          })
        }
      }
      else if (bd?.length === 1) {
        const sc = bd[0]!
        const param = collapseUntaggedScanlator(sc.scanlator, c.sourceName)
        rows.push({
          key: baseKey,
          candidate: c,
          scanlator: param,
          scanlatorParam: param,
          chapterCount: sc.count,
          chapterRanges: sc.ranges,
          coverageUnavailable: false,
          isSplit: false,
        })
      }
      else {
        rows.push({
          key: baseKey,
          candidate: c,
          scanlator: '',
          scanlatorParam: '',
          chapterCount: undefined,
          chapterRanges: '',
          // Only a definite failure (`null`) is "unavailable" — an absent key
          // (not yet fetched / still in flight) shows no coverage line at all.
          coverageUnavailable: bd === null,
          isSplit: false,
        })
      }
    }
    return rows
  })

  /** `configRows` merged with this row's current selection + rank state. */
  const displayRows = computed<DisplayRow[]>(() => {
    const sel = orderedKeys.value
    return configRows.value.map((row) => {
      const idx = sel.indexOf(row.key)
      return {
        ...row,
        selected: !!selected.value[row.key],
        rank: idx + 1,
        canUp: idx > 0,
        canDown: idx >= 0 && idx < sel.length - 1,
      }
    })
  })

  const toggleCand = (key: string): void => {
    const next = { ...selected.value, [key]: !selected.value[key] }
    selected.value = next
    if (next[key]) {
      if (!order.value.includes(key)) order.value = [...order.value, key]
    }
    else {
      order.value = order.value.filter(k => k !== key)
    }
  }

  // Move a selected candidate up (-1) or down (+1) within the selected ordering.
  const moveCand = (key: string, dir: -1 | 1): void => {
    const sel = orderedKeys.value
    const i = sel.indexOf(key)
    const j = i + dir
    if (i < 0 || j < 0 || j >= sel.length) return
    const full = [...order.value]
    const fi = full.indexOf(sel[i]!)
    const fj = full.indexOf(sel[j]!)
    ;[full[fi], full[fj]] = [full[fj]!, full[fi]!]
    order.value = full
  }

  /** Selected rows resolved to `{source, mangaId, scanlator}`, best-first. */
  const orderedProviders = computed<ProviderRef[]>(() =>
    orderedKeys.value.map((k) => {
      const row = configRows.value.find(r => r.key === k)!
      return { source: row.candidate.source, mangaId: row.candidate.mangaId, scanlator: row.scanlatorParam }
    }),
  )

  return {
    tray,
    trayActive,
    isGroupAdded,
    addGroup,
    removeGroup,
    removeCand,
    suggestedTrayTitle,
    configureTray,
    group,
    enterConfigure,
    displayRows,
    orderedKeys,
    selectedCount,
    toggleCand,
    moveCand,
    orderedProviders,
  }
}
