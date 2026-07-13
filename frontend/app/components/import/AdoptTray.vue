<script setup lang="ts">
import AppButton from '../ui/AppButton.vue'
import { candKey } from '../screens/import.types'
import type { SearchCandidate } from '../screens/import.types'

/**
 * AdoptTray — the persistent cross-search selection bar (Stage 1 of the Adopt
 * wizard). Shows every candidate gathered so far — across however many
 * searches under however many titles the same series turned up under — as a
 * removable source chip, plus the "Configure N sources →" action that carries
 * the whole tray into Stage 2 as one synthetic group. Rendered only while the
 * tray holds at least one candidate (`Import.vue`'s `trayActive`).
 *
 * Presentation-only — the tray contents arrive via `candidates` and every
 * action is emitted; `Import.vue` owns the actual tray state.
 */
defineProps<{
  /** Every candidate gathered into the tray so far. */
  candidates: SearchCandidate[]
}>()

const emit = defineEmits<{
  /** Carry the whole tray into Stage 2 (Configure) as one synthetic group. */
  configure: []
  /** Drop one candidate from the tray, by its `candKey` (`source:mangaId`). */
  remove: [key: string]
}>()

/** "1 source" vs "N sources" — used in both the count line and the CTA label. */
const sourceWord = (n: number): string => `${n} source${n === 1 ? '' : 's'}`
</script>

<template>
  <div class="tray">
    <div class="tray__info">
      <span class="tray__label">Selected across searches</span>
      <span class="tray__count">{{ sourceWord(candidates.length) }}</span>
    </div>

    <div class="tray__chips">
      <span v-for="c in candidates" :key="candKey(c)" class="tray__chip">
        {{ c.sourceName }}
        <button
          type="button"
          class="tray__chip-remove"
          :aria-label="`Remove ${c.sourceName}`"
          @click="emit('remove', candKey(c))"
        >
          ×
        </button>
      </span>
    </div>

    <AppButton variant="primary" @click="emit('configure')">
      Configure {{ sourceWord(candidates.length) }} →
    </AppButton>
  </div>
</template>

<style scoped>
.tray {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
  padding: 13px 16px;
  margin-bottom: 15px;
  border: 1px solid var(--accent);
  border-radius: var(--radius-xl);
  background: var(--accentSoft);
  box-shadow: var(--shadow-accent-sm);
}

.tray__info {
  display: flex;
  flex-direction: column;
  flex: none;
}

.tray__label {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--accentBright);
}

.tray__count {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.tray__chips {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
  flex: 1;
  min-width: 200px;
}

.tray__chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 5px 6px 5px 11px;
  border-radius: var(--radius-pill);
  background: var(--surface);
  border: 1px solid var(--border);
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--text);
  max-width: 100%;
  overflow-wrap: anywhere;
}

.tray__chip-remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 17px;
  height: 17px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: var(--surface3);
  color: var(--muted);
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
  transition: background 0.15s, color 0.15s;
}

.tray__chip-remove:hover {
  background: var(--danger-bg);
  color: var(--danger-bright);
}
</style>
