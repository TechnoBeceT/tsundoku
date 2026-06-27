<script setup lang="ts">
import CandidatePill from './CandidatePill.vue'
import type { SearchGroup } from '../screens/import.types'

/**
 * SearchGroupCard — one cross-source search group (Stage 1): a header with the
 * matched series title + a "N sources · choose →" count, over a wrapped row of
 * <CandidatePill>s (one per matched source). The whole card is a button; clicking
 * it picks the group to configure. Presentation-only — the group arrives via the
 * `group` prop and the click emits `pick`.
 */
defineProps<{
  /** The cross-source group this card represents. */
  group: SearchGroup
}>()

const emit = defineEmits<{
  /** The owner picked this group to configure + adopt (Stage 1 → Stage 2). */
  pick: [group: SearchGroup]
}>()
</script>

<template>
  <button type="button" class="group" @click="emit('pick', group)">
    <div class="group__head">
      <span class="group__title">{{ group.title }}</span>
      <span class="group__count">{{ group.candidates.length }} sources · choose →</span>
    </div>
    <div class="group__cands">
      <CandidatePill
        v-for="c in group.candidates"
        :key="`${c.source}:${c.mangaId}`"
        :candidate="c"
      />
    </div>
  </button>
</template>

<style scoped>
.group {
  display: block;
  width: 100%;
  text-align: left;
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: 15px;
  cursor: pointer;
  background: var(--surface2);
  transition: border-color 0.15s;
}

.group:hover {
  border-color: var(--accent);
}

.group__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 11px;
}

.group__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
}

.group__count {
  font-size: var(--text-xs);
  color: var(--accentBright);
  font-weight: var(--weight-bold);
  white-space: nowrap;
}

.group__cands {
  display: flex;
  gap: 9px;
  flex-wrap: wrap;
}
</style>
