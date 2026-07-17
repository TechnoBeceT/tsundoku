<script setup lang="ts">
import { computed } from 'vue'
import CandidatePill from './CandidatePill.vue'
import { candKey } from '../screens/import.types'
import type { SearchGroup } from '../screens/import.types'

/**
 * SearchGroupCard — one cross-source search group (Stage 1): a header with the
 * matched series title + a source count, over a wrapped row of <CandidatePill>s
 * (one per matched source), plus — in the Adopt wizard only — an "+ Add"/
 * "✓ Added" toggle for the cross-search adopt tray. Presentation-only — the
 * group arrives via the `group` prop and every action is emitted.
 *
 * `trayEnabled` gates the whole Add/Added toggle: the Adopt wizard
 * (`screens/Import.vue`) sets it true; the two SINGLE-SELECT match surfaces
 * that reuse this card (`scanLibrary/MatchPanel`,
 * `seriesDetail/MatchSourceDialog`) leave it off — they have no tray, so they
 * render just the classic pickable card with no stray toggle.
 *
 * Affordance rule (owner-approved — see the cross-search-adopt-tray spec; only
 * relevant when `trayEnabled`):
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
  /** Renders the adopt-tray Add/Added toggle; the single-select match surfaces leave it off. */
  trayEnabled?: boolean
  /** True when every candidate of this group is already in the adopt tray. */
  added?: boolean
  /** True once the tray holds at least one candidate — disables the card's own pick. */
  trayActive?: boolean
}>(), {
  trayEnabled: false,
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

/** True when the card body itself is the pick affordance (keyboard-operable). */
const bodyClickable = computed(() => !props.trayActive)

/** Card-body click: picks straight to Configure, but only while the tray is empty. */
function onCardClick(): void {
  if (bodyClickable.value) emit('pick', props.group)
}

/** The "+ Add" / "✓ Added" toggle — add when not yet fully tracked, else remove. */
function onToggle(): void {
  if (props.added) emit('remove', props.group)
  else emit('add', props.group)
}
</script>

<template>
  <div
    class="group"
    :class="{ 'group--static': trayActive }"
    :role="bodyClickable ? 'button' : undefined"
    :tabindex="bodyClickable ? 0 : undefined"
    @click="onCardClick"
    @keydown.enter="onCardClick"
    @keydown.space.prevent="onCardClick"
  >
    <div class="group__head">
      <span class="group__title">{{ group.title }}</span>
      <span class="group__count">
        {{ group.candidates.length }} sources<template v-if="bodyClickable"> · choose →</template>
      </span>
    </div>
    <div class="group__cands">
      <CandidatePill
        v-for="c in group.candidates"
        :key="candKey(c)"
        :candidate="c"
      />
    </div>
    <div v-if="trayEnabled" class="group__foot">
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
  padding: 0.9375rem; /* 15px @16 — off-ladder, byte-identical rem literal */
  cursor: pointer;
  background: var(--surface2);
  transition: border-color 0.15s;
}

.group:not(.group--static):hover {
  border-color: var(--accent);
}

/* Keyboard focus ring for the clickable card body (now a role="button" div). */
.group[tabindex]:focus-visible {
  outline: none;
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

/* trayActive: the card body no longer picks — no pointer/hover affordance. */
.group--static {
  cursor: default;
}

.group__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-sm); /* 10px @16 */
  margin-bottom: 0.6875rem; /* 11px @16 — off-ladder, byte-identical rem literal */
  flex-wrap: wrap;
}

.group__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  /* A long/unbroken title must wrap rather than push the card (and the page)
   * wider than the viewport (QCAT-230). */
  overflow-wrap: anywhere;
  min-width: 0;
}

.group__count {
  font-size: var(--text-xs);
  color: var(--accentBright);
  font-weight: var(--weight-bold);
  white-space: nowrap;
}

.group__cands {
  display: flex;
  gap: 0.5625rem; /* 9px @16 — off-ladder, byte-identical rem literal */
  flex-wrap: wrap;
}

.group__foot {
  display: flex;
  justify-content: flex-end;
  margin-top: 0.6875rem; /* 11px @16 — off-ladder, byte-identical rem literal */
}

.group__toggle {
  padding: var(--space-xs-tight) 0.8125rem; /* 6px 13px @16 (13px off-ladder) */
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
