<script setup lang="ts">
import { computed } from 'vue'

/**
 * ScoreSelector — a personal-score input that renders in whatever shape a
 * tracker's scale uses, so the same control fits AniList/MAL/Kitsu style scoring:
 *   - `point5`         — a 5-star row (click a star to set 1…5).
 *   - `point3`         — three faces: 😦 meh 🙂 → 1 / 2 / 3.
 *   - `point10`        — a row of ten number buttons 1…10.
 *   - `point10decimal` — a 0–10 slider in 0.5 steps with a numeric readout.
 *   - `point100`       — a 0–100 slider with a numeric readout.
 *
 * It is a controlled input (`v-model`): it emits `update:modelValue` and never
 * mutates its own value. `0` means "unscored" for every format. This is a
 * first-pass design surface — each format is a distinct, self-contained render.
 *
 * A11y (star/face/number rows — the `point5`/`point3`/`point10` shapes): each
 * row is a real WAI-ARIA radio group, not just a `role="radiogroup"` label
 * wrapping plain toggle buttons — every option carries `role="radio"` +
 * `aria-checked` (not `aria-pressed`, which is toggle-button semantics and
 * mismatches a `radiogroup` parent), and the group is a SINGLE tab stop:
 * only the checked option (or the first when unscored) has `tabindex="0"`,
 * every other option is `tabindex="-1"` (roving tabindex), and ArrowRight/
 * ArrowDown/ArrowLeft/ArrowUp/Home/End move + SELECT the focused option —
 * the same behaviour native `<input type="radio">` groups give for free. The
 * `point10decimal`/`point100` slider shapes were already correct (a native
 * `<input type="range">` is natively keyboard-operable with a proper
 * `aria-label` — no change needed there).
 *
 *   - `modelValue` (required): the current score (0 = unscored).
 *   - `format` (default `point10`): the scale to render.
 *   - `disabled`: blocks interaction + dims the control.
 */
const props = withDefaults(defineProps<{
  /** The current score (0 = unscored), interpreted per `format`. */
  modelValue: number
  /** Which scale to render. */
  format?: 'point100' | 'point10' | 'point10decimal' | 'point5' | 'point3'
  /** Blocks interaction + dims the control. */
  disabled?: boolean
}>(), {
  format: 'point10',
  disabled: false,
})

const emit = defineEmits<{
  /** The score changed — carries the new value. */
  'update:modelValue': [value: number]
}>()

const set = (value: number): void => {
  if (props.disabled) return
  // Clicking the current star/face/number again clears back to unscored.
  emit('update:modelValue', value === props.modelValue ? 0 : value)
}

const onSlider = (event: Event): void => {
  emit('update:modelValue', Number((event.target as HTMLInputElement).value))
}

const tens = Array.from({ length: 10 }, (_, i) => i + 1)
const faces = [
  { value: 1, icon: 'lucide:frown', label: 'Bad' },
  { value: 2, icon: 'lucide:meh', label: 'Okay' },
  { value: 3, icon: 'lucide:smile', label: 'Great' },
]

const isSlider = computed(() => props.format === 'point100' || props.format === 'point10decimal')
const sliderMax = computed(() => (props.format === 'point100' ? 100 : 10))
const sliderStep = computed(() => (props.format === 'point10decimal' ? 0.5 : 1))
const displayScore = computed(() => (props.modelValue === 0 ? '—' : props.modelValue))

/**
 * Roving-tabindex helper for a radio-group row: the checked option is the
 * one tab stop, or the row's FIRST option when nothing is scored yet (0) —
 * mirrors how a native radio group always keeps exactly one member tabbable.
 */
const rovingTabIndex = (value: number, firstValue: number): string =>
  value === (props.modelValue || firstValue) ? '0' : '-1'

/**
 * Arrow-key navigation for a radio-group row (WAI-ARIA radio pattern):
 * Right/Down moves to the next option, Left/Up to the previous (both wrap),
 * Home/End jump to the first/last. Movement both FOCUSES and SELECTS the
 * target option — the same behaviour a native `<input type="radio">` group
 * gives for free. Reads sibling buttons off the DOM (all options in a row
 * are direct siblings) rather than re-deriving the value list per format.
 */
function onGroupKeydown(event: KeyboardEvent): void {
  if (props.disabled) return
  const nav: Record<string, (i: number, n: number) => number> = {
    ArrowRight: (i, n) => (i + 1) % n,
    ArrowDown: (i, n) => (i + 1) % n,
    ArrowLeft: (i, n) => (i - 1 + n) % n,
    ArrowUp: (i, n) => (i - 1 + n) % n,
    Home: () => 0,
    End: (_i, n) => n - 1,
  }
  const step = nav[event.key]
  if (!step) return
  const group = (event.currentTarget as HTMLElement).parentElement
  if (!group) return
  const options = Array.from(group.children) as HTMLButtonElement[]
  const current = options.indexOf(event.currentTarget as HTMLButtonElement)
  if (current === -1) return
  event.preventDefault()
  const next = options[step(current, options.length)]
  next?.focus()
  next?.click()
}
</script>

<template>
  <div class="score" :class="{ 'score--disabled': disabled }">
    <!-- 5-star row -->
    <div v-if="format === 'point5'" class="score__row" role="radiogroup" aria-label="Score out of 5">
      <button
        v-for="n in 5"
        :key="n"
        type="button"
        role="radio"
        class="score__star"
        :class="{ 'score__star--on': n <= modelValue }"
        :disabled="disabled"
        :aria-label="`${n} star${n > 1 ? 's' : ''}`"
        :aria-checked="n === modelValue"
        :tabindex="rovingTabIndex(n, 1)"
        @click="set(n)"
        @keydown="onGroupKeydown"
      >
        <Icon name="lucide:star" />
      </button>
    </div>

    <!-- 3-face row -->
    <div v-else-if="format === 'point3'" class="score__row" role="radiogroup" aria-label="Score out of 3">
      <button
        v-for="f in faces"
        :key="f.value"
        type="button"
        role="radio"
        class="score__face"
        :class="{ 'score__face--on': f.value === modelValue }"
        :disabled="disabled"
        :aria-label="f.label"
        :aria-checked="f.value === modelValue"
        :tabindex="rovingTabIndex(f.value, 1)"
        @click="set(f.value)"
        @keydown="onGroupKeydown"
      >
        <Icon :name="f.icon" />
      </button>
    </div>

    <!-- 10 number buttons -->
    <div v-else-if="format === 'point10'" class="score__row score__row--tens" role="radiogroup" aria-label="Score out of 10">
      <button
        v-for="n in tens"
        :key="n"
        type="button"
        role="radio"
        class="score__num"
        :class="{ 'score__num--on': n <= modelValue }"
        :disabled="disabled"
        :aria-label="`${n} out of 10`"
        :aria-checked="n === modelValue"
        :tabindex="rovingTabIndex(n, 1)"
        @click="set(n)"
        @keydown="onGroupKeydown"
      >{{ n }}</button>
    </div>

    <!-- slider (point10decimal / point100) -->
    <div v-else-if="isSlider" class="score__slider">
      <input
        class="score__range"
        type="range"
        min="0"
        :max="sliderMax"
        :step="sliderStep"
        :value="modelValue"
        :disabled="disabled"
        :aria-label="`Score out of ${sliderMax}`"
        @input="onSlider"
      >
      <span class="score__readout">{{ displayScore }}<span class="score__outof">/{{ sliderMax }}</span></span>
    </div>
  </div>
</template>

<style scoped>
.score {
  display: inline-flex;
  align-items: center;
}

.score--disabled {
  opacity: 0.55;
}

.score__row {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.score__row--tens {
  gap: 5px;
  flex-wrap: wrap;
}

/* ---- Stars ---------------------------------------------------------------- */
.score__star {
  display: inline-flex;
  padding: 3px;
  border: none;
  background: none;
  color: var(--faint);
  font-size: 20px;
  cursor: pointer;
  transition: color 0.12s, transform 0.12s;
}

.score__star--on {
  color: var(--accentBright);
}

.score__star:hover:not(:disabled) {
  color: var(--accent);
  transform: scale(1.12);
}

/* ---- Faces ---------------------------------------------------------------- */
.score__face {
  display: inline-flex;
  padding: 6px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--surface2);
  color: var(--muted);
  font-size: 20px;
  cursor: pointer;
  transition: color 0.12s, background 0.12s, border-color 0.12s;
}

.score__face--on {
  border-color: var(--accent);
  background: var(--accentSoft);
  color: var(--accentBright);
}

.score__face:hover:not(:disabled) {
  color: var(--text);
  border-color: var(--accent);
}

/* ---- 10 number buttons ---------------------------------------------------- */
.score__num {
  min-width: 30px;
  height: 30px;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border2);
  background: var(--surface2);
  color: var(--muted);
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: color 0.12s, background 0.12s, border-color 0.12s;
}

.score__num--on {
  border-color: var(--accent);
  background: var(--accentSoft);
  color: var(--accentBright);
}

.score__num:hover:not(:disabled) {
  border-color: var(--accent);
  color: var(--text);
}

/* ---- Slider --------------------------------------------------------------- */
.score__slider {
  display: inline-flex;
  align-items: center;
  gap: 12px;
}

.score__range {
  width: 180px;
  accent-color: var(--accent);
  cursor: pointer;
}

.score__readout {
  min-width: 52px;
  font-family: var(--font-mono);
  font-size: var(--text-md);
  font-weight: var(--weight-bold);
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.score__outof {
  color: var(--faint);
  font-weight: var(--weight-regular);
}

.score button:disabled,
.score__range:disabled {
  cursor: default;
}

.score button:focus-visible,
.score__range:focus-visible {
  outline: none;
  border-radius: var(--radius-sm);
  box-shadow: var(--ring-focus);
}
</style>
