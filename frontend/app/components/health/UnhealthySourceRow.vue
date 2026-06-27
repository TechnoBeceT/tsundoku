<script setup lang="ts">
import { computed } from 'vue'
import Chip from '../ui/Chip.vue'
import HealthBadge from '../ui/HealthBadge.vue'
import type { Provider } from '../screens/seriesDetail.types'

/**
 * UnhealthySourceRow — one unhealthy source line inside a SickSeriesCard: the
 * provider name, a language Chip, a HealthBadge for the stale/erroring state, a
 * relative last-synced label, an optional "N behind" note, and the inline last
 * error. Presentation-only — the whole source arrives via the `source` prop and
 * the row emits nothing.
 *
 * Token-only colours (HealthBadge reads the shared `--sd-hl-*` health tokens),
 * so it renders correctly in both themes.
 */
const props = defineProps<{
  /** The unhealthy source to render (a Series Detail Provider row). */
  source: Provider
}>()

// Relative-time label for the last-synced timestamp (mirrors Series Detail).
const rel = (iso: string | null): string => {
  if (iso == null) return 'never'
  const d = Date.now() - Date.parse(iso)
  const m = 60_000, h = 3_600_000, day = 86_400_000
  if (d < m) return 'just now'
  if (d < h) return `${Math.floor(d / m)}m ago`
  if (d < day) return `${Math.floor(d / h)}h ago`
  return `${Math.floor(d / day)}d ago`
}

// Uppercased language code shown in the Chip (e.g. "EN").
const language = computed(() => props.source.language.toUpperCase())
// null last-synced reads "never synced" (not "synced never").
const syncedLabel = computed(() =>
  props.source.lastSyncedAt == null ? 'never synced' : `synced ${rel(props.source.lastSyncedAt)}`)
// Only show the chapters-behind note when this source is actually behind.
const hasBehind = computed(() => props.source.chaptersBehind > 0)
</script>

<template>
  <div class="source">
    <span class="source__provider">{{ source.provider }}</span>
    <Chip variant="language">{{ language }}</Chip>
    <HealthBadge :health="source.health" />
    <span class="source__synced">{{ syncedLabel }}</span>
    <span v-if="hasBehind" class="source__behind">· {{ source.chaptersBehind }} behind</span>
    <div v-if="source.lastError" class="source__error">{{ source.lastError }}</div>
  </div>
</template>

<style scoped>
.source {
  display: flex;
  align-items: center;
  gap: 9px;
  flex-wrap: wrap;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--surface2);
}

.source + .source {
  margin-top: 8px;
}

.source__provider {
  font-weight: var(--weight-bold);
  font-size: var(--text-base);
  color: var(--text);
}

.source__synced {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__behind {
  font-size: var(--text-xs);
  color: var(--faint);
}

.source__error {
  flex-basis: 100%;
  margin-top: 3px;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--sd-hl-erroring-fg);
  word-break: break-word;
}
</style>
