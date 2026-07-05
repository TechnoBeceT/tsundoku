<script setup lang="ts">
import CandidatePill from './CandidatePill.vue'
import { candKey } from '../screens/import.types'
import type { SearchGroup } from '../screens/import.types'

/**
 * SearchGroupCard — one cross-source search group (Stage 1): a header with the
 * matched series title + a source count, over a wrapped row of <CandidatePill>s
 * (one per matched source), plus an "+ Add"/"✓ Added" toggle for the cross-search
 * adopt tray. Presentation-only — the group arrives via the `group` prop and
 * every action is emitted.
 *
 * Affordance rule (owner-approved — see the cross-search-adopt-tray spec):
 *   - `trayActive` false (nothing gathered yet): the whole card is still the
 *     classic one-tap "choose →" straight to Configure (nothing regresses for
 *     the common single-group case) — clicking anywhere but the toggle emits
 *     `pick`. The toggle also works here, to START gathering into the tray.
 *   - `trayActive` true (the owner is mid-gather): the card no longer picks —
 *     the ONLY way into Configure is the tray bar's "Configure N sources →" —
 *     so a stray tap can't silently bypass the in-progress selection. Only the
 *     toggle (`add`/`remove`) responds.
 * `added` (every candidate of this group already in the tray) flips the toggle
 * to "✓ Added"; clicking it then removes the whole group from the tray.
 */
const props = withDefaults(defineProps<{
  /** The cross-source group this card represents. */
  group: SearchGroup
  /** True when every candidate of this group is already in the adopt tray. */
  added?: boolean
  /** True once the tray holds at least one candidate — disables the card's own pick. */
  trayActive?: boolean
}>(), {
  added: false,
  trayActive: false,
})

const emit = defineEmits<{
  /** The owner picked this group to configure + adopt directly (classic path, tray empty). */
  pick: [group: SearchGroup]
  /** Add every not-yet-tracked candidate of this group to the adopt tray. */
  add: [group: SearchGroup]
  /** Drop every candidate of this group from the adopt tray. */
  remove: [group: SearchGroup]
}>()

/** Card-body click: picks straight to Configure, but only while the tray is empty. */
function onCardClick(): void {
  if (!props.trayActive) emit('pick', props.group)
}

/** The "+ Add" / "✓ Added" toggle — add when not yet fully tracked, else remove. */
function onToggle(): void {
  emit(props.added ? 'remove' : 'add', props.group)
}
</script>

<template>
  <div class="group" :class="{ 'group--static': trayActive }" @click="onCardClick">
    <div class="group__head">
      <span class="group__title">{{ group.title }}</span>
      <span class="group__count">
        {{ group.candidates.length }} sources<template v-if="!trayActive"> · choose →</template>
      </span>
    </div>
    <div class="group__cands">
      <CandidatePill
        v-for="c in group.candidates"
        :key="candKey(c)"
        :candidate="c"
      />
    </div>
    <div class="group__foot">
      <button
        type="button"
        class="group__toggle"
        :class="{ 'group__toggle--added': added }"
        @click.stop="onToggle"
      >
        {{ added ? '✓ Added' : '+ Add' }}
      </button>
    </div>
  </div>
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

.group:not(.group--static):hover {
  border-color: var(--accent);
}

/* trayActive: the card body no longer picks — no pointer/hover affordance. */
.group--static {
  cursor: default;
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

.group__foot {
  display: flex;
  justify-content: flex-end;
  margin-top: 11px;
}

.group__toggle {
  padding: 6px 13px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border2);
  background: var(--surface);
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: all 0.15s;
}

.group__toggle:hover {
  border-color: var(--accent);
  color: var(--accentBright);
}

.group__toggle--added {
  border-color: var(--accent);
  background: var(--accentSoft);
  color: var(--accentBright);
}
</style>
