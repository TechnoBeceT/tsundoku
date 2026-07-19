<script setup lang="ts">
import { computed } from 'vue'
import Chip from '../ui/Chip.vue'
import HealthBadge from '../ui/HealthBadge.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import Toggle from '../ui/Toggle.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Provider } from '../screens/seriesDetail.types'

/**
 * ProviderRow — one ranked source in the Series-Detail "Sources" list: a
 * `ReorderControl` rank stepper, the source name (+ a PREFERRED chip for rank 1,
 * or an UNLINKED chip for a disk-origin group), the language/scanlator/
 * importance meta line, the source's chapter coverage, a `HealthBadge` with the
 * chapters-behind note, the synced/newest timestamps, an optional last-error,
 * and the row actions (a quiet Remove button, plus — for an unlinked disk-origin
 * group only — a "Match to source" button). Presentation-only — the source + its
 * rank arrive via props; the row emits `move` (re-rank), `remove`, and `match`
 * (opens `MatchDiskProviderDialog` for this provider).
 *
 * `provider.linked` is false for a disk-origin group created by library
 * import (no real Suwayomi source attached — `suwayomi_id=0` on the backend).
 *
 * COVERAGE — the two numbers say different things, and the row must never blur
 * them (a bare "56 chapters" once read as the source's offering and misled a
 * live diagnosis):
 *   - `feedCount` / `feedRanges` = what this source OFFERS ("270 chapters ·
 *     1-269"), straight from the stored ProviderChapter feed on the series-detail
 *     response. NO click, NO fetch — in particular no live ping to the source
 *     (we already hold the feed; pinging for it is needless ban risk).
 *   - `chapterCount` = how many of the owner's downloaded files this source
 *     currently SUPPLIES ("supplies 56").
 * A provider with an empty feed (`feedCount === 0` — e.g. an unlinked disk-origin
 * group) shows "No chapter feed" rather than a phantom "0 chapters".
 *
 * FRACTIONAL EVIDENCE — shown ONLY when the source actually has fractionals
 * (`fractionalCount > 0`), and shown IN FULL (capped for sanity, never hidden
 * behind a disclosure): some mirrors re-upload a whole chapter N as a lone
 * "N.1" under their own URL, and nothing but the list can tell that apart from a
 * genuine `5.5` side-chapter. A re-uploader shows a long SYSTEMATIC run (1.1,
 * 2.1, 3.1, …); an omake source shows a lone 5.5. The `ignoreFractional` toggle
 * rides directly under that evidence — a switch with no evidence behind it is a
 * blind switch, and the owner rejected exactly that. It is a SUPPRESSION switch,
 * not a delete: downloaded files and existing chapters are kept.
 */
const props = defineProps<{
  /** The source to render. */
  provider: Provider
  /** 1-based display rank (top = preferred). */
  rank: number
  /** Whether this is the rank-1 / preferred source (drives the chip + highlight). */
  preferred: boolean
  /** Whether the up arrow is enabled (false = already top). */
  canUp: boolean
  /** Whether the down arrow is enabled (false = already bottom). */
  canDown: boolean
  /** True while a mutation is in flight — disables reorder + remove. */
  saving?: boolean
  /** True when this row is an unlinked disk provider with a mergeable linked twin (drift). Renders a DUPLICATE chip. */
  duplicate?: boolean
  /** True to render the multi-select checkbox (consolidation mode). */
  selectable?: boolean
  /** Whether this row is currently selected for consolidation (only meaningful when selectable). */
  selected?: boolean
}>()

const emit = defineEmits<{
  /** A re-rank was requested: -1 = up (raise), 1 = down (lower). */
  move: [direction: MoveDirection]
  /** The Remove button was pressed. */
  remove: []
  /** The "Match to source" button was pressed (unlinked groups only). */
  match: []
  /** The ignore-fractional switch flipped — carries the NEW value. */
  toggleIgnoreFractional: [ignore: boolean]
  /** The consolidation checkbox flipped — carries the NEW selected value. */
  toggleSelect: [selected: boolean]
}>()

// How many fractional numbers the evidence line renders before collapsing the
// rest into "+N more". The COUNT beside it always tells the whole truth, so a
// pathological 300-fractional feed can never blow up the row.
const FRACTIONAL_PREVIEW = 12

// Uppercased language code shown in the language Chip (e.g. "EN").
const language = computed(() => props.provider.language.toUpperCase())

// What the SOURCE offers: "270 chapters · 1-269" (ranges omitted when the feed
// carries no chapter numbers). Empty feed → null, so the row can say so plainly
// instead of rendering "0 chapters".
const offering = computed<string | null>(() => {
  const { feedCount, feedRanges } = props.provider
  if (feedCount <= 0) return null
  const label = `${feedCount} chapter${feedCount === 1 ? '' : 's'}`
  return feedRanges ? `${label} · ${feedRanges}` : label
})

// The fractional evidence: the source's fractional chapter numbers, in order —
// "1.1, 2.1, 3.1 …" for a re-uploader, a lone "5.5" for a genuine omake. Capped
// at FRACTIONAL_PREVIEW with a "+N more" tail. Null when the source has none, so
// the row renders neither the line nor the toggle.
const fractionalSummary = computed<string | null>(() => {
  const list = props.provider.fractionalChapters
  if (!list?.length) return null
  const shown = list.slice(0, FRACTIONAL_PREVIEW).join(', ')
  const rest = list.length - FRACTIONAL_PREVIEW
  return rest > 0 ? `${shown} +${rest} more` : shown
})

// Relative-time label for the sync/newest timestamps (null → "never").
const rel = (iso: string | null): string => {
  if (iso == null) return 'never'
  const d = Date.now() - Date.parse(iso)
  const m = 60_000, h = 3_600_000, day = 86_400_000
  if (d < m) return 'just now'
  if (d < h) return `${Math.floor(d / m)}m ago`
  if (d < day) return `${Math.floor(d / h)}h ago`
  return `${Math.floor(d / day)}d ago`
}
</script>

<template>
  <div class="source" :class="{ 'source--selected': selectable && selected }">
    <!-- Consolidation multi-select: only rendered in selectable mode so the
         ordinary Sources list is visually unchanged. -->
    <label v-if="selectable" class="source__select">
      <input
        type="checkbox"
        :checked="selected"
        :aria-label="`Select ${provider.providerName} for merge`"
        @change="emit('toggleSelect', ($event.target as HTMLInputElement).checked)"
      >
    </label>
    <ReorderControl
      :rank="rank"
      :top-highlighted="preferred"
      :can-up="canUp"
      :can-down="canDown"
      :disabled="saving"
      @move="emit('move', $event)"
    />

    <div class="source__main">
      <div class="source__namerow">
        <span class="source__name">{{ provider.providerName }}</span>
        <Chip v-if="preferred" variant="accent">PREFERRED</Chip>
        <Chip v-if="!provider.linked" variant="neutral">UNLINKED</Chip>
        <Chip v-if="duplicate" variant="accent">DUPLICATE</Chip>
      </div>
      <div class="source__meta">
        <Chip variant="language">{{ language }}</Chip>
        <span v-if="provider.scanlator">{{ provider.scanlator }}</span>
        <span>importance {{ provider.importance }}</span>
      </div>
      <div class="source__coverage">
        <span v-if="offering" class="source__offering">{{ offering }}</span>
        <span v-else class="source__offering source__offering--none">No chapter feed</span>
        <span class="source__supplies">supplies {{ provider.chapterCount }}</span>
      </div>
      <!-- Fractional evidence + the suppression switch — only for a source that
           HAS fractionals (no evidence ⇒ no switch, by design). -->
      <template v-if="provider.fractionalCount > 0">
        <div class="source__fractional">
          <span class="source__fractional-count">
            {{ provider.fractionalCount }} fractional
          </span>
          <span class="source__fractional-list">{{ fractionalSummary }}</span>
        </div>
        <label class="source__fractional-toggle">
          <!-- eslint-disable vue/attribute-hyphenation -->
          <!-- camelCase :ariaLabel binds Toggle's REQUIRED prop; kebab :aria-label
               would land as a plain HTML attribute and leave the prop unset
               (vue-tsc catches it — same convention as SeriesHeader's toggles). -->
          <Toggle
            :model-value="provider.ignoreFractional"
            :disabled="saving"
            :ariaLabel="'Ignore fractional chapters from this source'"
            @update:model-value="emit('toggleIgnoreFractional', $event)"
          />
          <span class="source__fractional-label">Ignore fractional chapters from this source</span>
        </label>
        <div class="source__fractional-note">
          Stops NEW fractional downloads from this source. Downloaded files and existing chapters are kept — un-tick to restore it.
        </div>
      </template>

      <div v-if="!provider.linked" class="source__unlinked-note">
        Imported from disk — no real source attached. Match it to link these chapters without re-downloading.
      </div>
      <div class="source__healthrow">
        <HealthBadge :health="provider.health" />
        <span v-if="provider.chaptersBehind > 0" class="source__behind">{{ provider.chaptersBehind }} behind</span>
      </div>
      <!-- Actionable hint for a source whose extension is gone: the badge alone
           doesn't say WHAT to do, and this was invisible before (a source showed
           "Healthy · supplies 0" while its extension was uninstalled). -->
      <div v-if="provider.health === 'unavailable'" class="source__unavailable-note">
        Extension not installed — reinstall or remove.
      </div>
      <div class="source__times">
        <span>Synced {{ rel(provider.lastSyncedAt) }}</span>
        <span>Newest {{ rel(provider.newestChapterAt) }}</span>
      </div>
      <div v-if="provider.lastError" class="source__error">{{ provider.lastError }}</div>
      <div class="source__actions">
        <button v-if="!provider.linked" type="button" class="btn-match" :disabled="saving" @click="emit('match')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M9 18l6-6-6-6" />
          </svg>
          Match to source
        </button>
        <button type="button" class="btn-remove" :disabled="saving" @click="emit('remove')">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
          </svg>
          Remove
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Off-ladder raw px in visible properties migrated to exact spacing tokens /
 * byte-identical rem (value÷16) — design px at the 16px desktop anchor, fluid
 * on a phone. */
.source {
  display: flex;
  align-items: flex-start;
  gap: 0.6875rem; /* 11px */
  margin-bottom: var(--space-sm); /* 10px */
  padding: var(--space-md); /* 12px */
  border-radius: 0.8125rem; /* 13px */
  border: 1px solid var(--border);
  background: var(--surface2);
}

/* Consolidation multi-select highlight + checkbox column. */
.source--selected {
  border-color: var(--accent);
  background: var(--accentSoft);
}

.source__select {
  display: flex;
  align-items: center;
  padding-top: 0.125rem;
  cursor: pointer;
}

.source__select input {
  width: 1rem;
  height: 1rem;
  cursor: pointer;
  accent-color: var(--accent);
}

.source__main {
  flex: 1;
  min-width: 0;
}

.source__namerow {
  display: flex;
  align-items: center;
  gap: var(--space-xs); /* 8px */
  margin-bottom: 0.3125rem; /* 5px */
  flex-wrap: wrap;
}

.source__name {
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.source__meta {
  display: flex;
  align-items: center;
  gap: 0.4375rem; /* 7px */
  margin-bottom: var(--space-xs); /* 8px */
  flex-wrap: wrap;
  font-size: 0.71875rem; /* 11.5px */
  color: var(--muted);
}

.source__healthrow {
  display: flex;
  align-items: center;
  gap: var(--space-xs); /* 8px */
  flex-wrap: wrap;
}

.source__behind {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__coverage {
  display: flex;
  align-items: baseline;
  gap: var(--space-xs); /* 8px */
  flex-wrap: wrap;
  margin-bottom: var(--space-xs); /* 8px */
  font-size: 0.71875rem; /* 11.5px */
}

/* What the SOURCE offers — the headline number, so it can't be misread as the
   satisfied count sitting next to it. */
.source__offering {
  color: var(--text);
  font-weight: var(--weight-bold);
}

.source__offering--none {
  color: var(--faint);
  font-weight: var(--weight-regular);
}

/* How many downloaded files come FROM this source — deliberately quieter. */
.source__supplies {
  color: var(--faint);
}

/* The fractional evidence line — deliberately LOUD (a warning tint): it is the
   only thing that lets the owner tell a re-uploader from a real side-chapter. */
.source__fractional {
  display: flex;
  align-items: baseline;
  gap: 0.4375rem; /* 7px */
  flex-wrap: wrap;
  margin-bottom: 0.4375rem; /* 7px */
  padding: 0.3125rem 0.5625rem; /* 5px 9px */
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: var(--surface3);
  font-size: 0.71875rem; /* 11.5px */
}

.source__fractional-count {
  color: var(--text);
  font-weight: var(--weight-bold);
  white-space: nowrap;
}

.source__fractional-list {
  color: var(--muted);
  font-family: var(--font-mono);
  word-break: break-word;
}

.source__fractional-toggle {
  display: flex;
  align-items: center;
  gap: 0.5625rem; /* 9px */
  cursor: pointer;
}

.source__fractional-label {
  font-size: 0.71875rem; /* 11.5px */
  color: var(--text);
}

.source__fractional-note {
  margin-top: var(--space-2xs); /* 4px */
  margin-bottom: var(--space-xs); /* 8px */
  font-size: 0.65625rem; /* 10.5px */
  line-height: 1.4;
  color: var(--faint);
}

.source__times {
  display: flex;
  gap: var(--space-base); /* 14px */
  flex-wrap: wrap;
  margin-top: var(--space-xs); /* 8px */
  font-size: 0.65625rem; /* 10.5px */
  color: var(--faint);
}

.source__error {
  margin-top: var(--space-xs); /* 8px */
  padding: var(--space-xs-tight) 0.5625rem; /* 6px 9px */
  border-radius: var(--radius-sm);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-text);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  word-break: break-word;
}

.source__unlinked-note {
  margin-top: var(--space-xs-tight); /* 6px */
  margin-bottom: var(--space-xs); /* 8px */
  font-size: 0.71875rem; /* 11.5px */
  line-height: 1.4;
  color: var(--muted);
}

/* The "extension gone" hint — tinted with the unavailable health token so it
   reads as the same "needs action" state as its badge. */
.source__unavailable-note {
  margin-top: var(--space-xs-tight); /* 6px */
  font-size: 0.71875rem; /* 11.5px */
  line-height: 1.4;
  color: var(--sd-hl-unavailable-fg);
}

.source__actions {
  display: flex;
  gap: var(--space-xs); /* 8px */
  margin-top: 0.5625rem; /* 9px */
}

.btn-remove,
.btn-match {
  display: flex;
  align-items: center;
  gap: 0.3125rem; /* 5px */
  padding: 0.3125rem var(--space-sm); /* 5px 10px */
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: transparent;
  font-size: 0.71875rem; /* 11.5px */
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: background 0.15s, border-color 0.15s;
}

.btn-remove {
  color: var(--danger-bright);
}

.btn-remove:hover {
  background: var(--danger-bg);
}

.btn-match {
  color: var(--accentBright);
  border-color: var(--accent);
}

.btn-match:hover {
  background: var(--accentSoft);
}

.btn-remove:disabled,
.btn-match:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
