<script setup lang="ts">
import { computed } from 'vue'
import type { StepItem } from './nav.types'

/**
 * Stepper — a numbered step indicator with done / active / todo states. Every
 * step before `current` is "done" (a check), the step at `current` is "active"
 * (accent), and every step after is "todo" (muted).
 *
 * Two layouts via `orientation`:
 *   - `horizontal` (default) — pills joined by a connector line (the Import
 *     Search → Configure → Adopt flow).
 *   - `vertical` — stacked rows each with a status dot (the Settings engine
 *     upgrade stepper).
 *
 * Props:
 *   - `steps`       — ordered `{ key, label, sub? }`; `sub` is a secondary line.
 *   - `current`     — the active step's `key`.
 *   - `orientation` — `'horizontal'` | `'vertical'` (default `'horizontal'`).
 */
const props = withDefaults(defineProps<{
  /** Ordered steps to render. */
  steps: StepItem[]
  /** The active step's key. */
  current: string
  /** Layout direction. */
  orientation?: 'horizontal' | 'vertical'
}>(), {
  orientation: 'horizontal',
})

type StepStatus = 'done' | 'active' | 'todo'

// Resolve each step's status once (relative to `current`): everything before the
// active step is done, the match is active, everything after is todo. An unknown
// `current` leaves every step as todo.
const decorated = computed<(StepItem & { status: StepStatus })[]>(() => {
  const currentIndex = props.steps.findIndex(s => s.key === props.current)
  return props.steps.map((s, i) => ({
    ...s,
    status: i < currentIndex ? 'done' : i === currentIndex ? 'active' : 'todo',
  }))
})
</script>

<template>
  <!-- ===================== HORIZONTAL ===================== -->
  <div v-if="orientation === 'horizontal'" class="stepper stepper--h">
    <template v-for="(s, i) in decorated" :key="s.key">
      <div class="hstep" :class="`hstep--${s.status}`">
        <span class="hstep__num">
          <svg v-if="s.status === 'done'" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
          <template v-else>{{ i + 1 }}</template>
        </span>
        <span class="hstep__text">
          {{ s.label }}
          <span v-if="s.sub" class="hstep__sub">{{ s.sub }}</span>
        </span>
      </div>
      <div v-if="i < decorated.length - 1" class="stepper__line" />
    </template>
  </div>

  <!-- ===================== VERTICAL ===================== -->
  <div v-else class="stepper stepper--v">
    <div v-for="(s, i) in decorated" :key="s.key" class="vstep">
      <span class="vstep__dot" :class="`vstep__dot--${s.status}`">
        <svg v-if="s.status === 'done'" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
        <span v-else class="vstep__num">{{ i + 1 }}</span>
      </span>
      <span class="vstep__text">
        {{ s.label }}
        <span v-if="s.sub" class="vstep__sub">{{ s.sub }}</span>
      </span>
    </div>
  </div>
</template>

<style scoped>
/* ---- Horizontal (pills + connector) --------------------------------------- */
.stepper--h {
  display: flex;
  align-items: center;
  gap: var(--space-2xs); /* 4px @16 */
}

.stepper__line {
  flex: 1;
  height: 1px;
  background: var(--border);
}

.hstep {
  display: flex;
  align-items: center;
  gap: var(--space-xs); /* 8px @16 */
  padding: var(--space-xs) 0.8125rem; /* 8px 13px @16 (13px off-ladder) */
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--muted);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  white-space: nowrap;
}

.hstep--active {
  background: var(--accentSoft);
  color: var(--accentBright);
}

.hstep__num {
  width: 1.3125rem; /* 21px @16 */
  height: 1.3125rem; /* 21px @16 */
  border-radius: 50%;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  background: var(--surface3);
  color: var(--faint);
}

.hstep--active .hstep__num,
.hstep--done .hstep__num {
  background: var(--accent);
  color: var(--cover-text);
}

.hstep__text {
  display: flex;
  flex-direction: column;
}

.hstep__sub {
  font-size: var(--text-xs);
  font-weight: var(--weight-regular);
  color: var(--muted);
}

/* ---- Vertical (stacked rows + dot) ---------------------------------------- */
.stepper--v {
  display: flex;
  flex-direction: column;
  background: var(--surface2);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: var(--space-base) var(--space-lg); /* 14px 16px @16 */
}

.vstep {
  display: flex;
  align-items: center;
  gap: var(--space-md); /* 12px @16 */
  padding: var(--space-xs-tight) 0; /* 6px @16 */
}

.vstep__dot {
  width: 1.625rem; /* 26px @16 */
  height: 1.625rem; /* 26px @16 */
  border-radius: 50%;
  flex: none;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--surface3);
  color: var(--faint);
}

.vstep__dot--done {
  background: var(--set-ok-bg);
  color: var(--set-ok-dot);
}

.vstep__dot--active {
  background: var(--accentSoft);
  color: var(--accentBright);
}

.vstep__num {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.vstep__text {
  display: flex;
  flex-direction: column;
  font-size: 0.84375rem; /* 13.5px @16 — off-ladder, byte-identical rem literal */
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.vstep__sub {
  font-size: var(--text-xs);
  font-weight: var(--weight-regular);
  color: var(--muted);
}
</style>
