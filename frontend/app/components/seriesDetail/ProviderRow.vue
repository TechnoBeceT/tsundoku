<script setup lang="ts">
import { computed } from 'vue'
import Chip from '../ui/Chip.vue'
import HealthBadge from '../ui/HealthBadge.vue'
import ReorderControl from '../ui/ReorderControl.vue'
import type { MoveDirection } from '../ui/controls.types'
import type { Provider } from '../screens/seriesDetail.types'

/**
 * ProviderRow — one ranked source in the Series-Detail "Sources" list: a
 * `ReorderControl` rank stepper, the source name (+ a PREFERRED chip for rank 1),
 * the language/scanlator/importance meta line, a `HealthBadge` with the
 * chapters-behind note, the synced/newest timestamps, an optional last-error, and
 * a quiet Remove button. Presentation-only — the source + its rank arrive via
 * props; the row emits `move` (re-rank) and `remove`.
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
}>()

const emit = defineEmits<{
  /** A re-rank was requested: -1 = up (raise), 1 = down (lower). */
  move: [direction: MoveDirection]
  /** The Remove button was pressed. */
  remove: []
}>()

// Uppercased language code shown in the language Chip (e.g. "EN").
const language = computed(() => props.provider.language.toUpperCase())

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
  <div class="source">
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
      </div>
      <div class="source__meta">
        <Chip variant="language">{{ language }}</Chip>
        <span v-if="provider.scanlator">{{ provider.scanlator }}</span>
        <span>importance {{ provider.importance }}</span>
      </div>
      <div class="source__healthrow">
        <HealthBadge :health="provider.health" />
        <span v-if="provider.chaptersBehind > 0" class="source__behind">{{ provider.chaptersBehind }} behind</span>
      </div>
      <div class="source__times">
        <span>Synced {{ rel(provider.lastSyncedAt) }}</span>
        <span>Newest {{ rel(provider.newestChapterAt) }}</span>
      </div>
      <div v-if="provider.lastError" class="source__error">{{ provider.lastError }}</div>
      <div class="source__actions">
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
.source {
  display: flex;
  align-items: flex-start;
  gap: 11px;
  margin-bottom: 10px;
  padding: 12px;
  border-radius: 13px;
  border: 1px solid var(--border);
  background: var(--surface2);
}

.source__main {
  flex: 1;
  min-width: 0;
}

.source__namerow {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 5px;
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
  gap: 7px;
  margin-bottom: 8px;
  flex-wrap: wrap;
  font-size: 11.5px;
  color: var(--muted);
}

.source__healthrow {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.source__behind {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__times {
  display: flex;
  gap: 14px;
  flex-wrap: wrap;
  margin-top: 8px;
  font-size: 10.5px;
  color: var(--faint);
}

.source__error {
  margin-top: 8px;
  padding: 6px 9px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-text);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  word-break: break-word;
}

.source__actions {
  margin-top: 9px;
}

.btn-remove {
  display: flex;
  align-items: center;
  gap: 5px;
  padding: 5px 10px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border);
  background: transparent;
  color: var(--danger-bright);
  font-size: 11.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: background 0.15s;
}

.btn-remove:hover {
  background: var(--danger-bg);
}

.btn-remove:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
