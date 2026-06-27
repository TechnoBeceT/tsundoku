<script setup lang="ts">
/**
 * SettingRow — a labelled settings row: a bold name + faint hint on the left and
 * a trailing control (the default slot) on the right, split across a top-bordered
 * row. The one shape behind every "label … control" line that the Library,
 * Engine, and Extensions panes each hand-rolled (the duration pickers, the
 * integer fields, the engine running-version status line, the extension
 * update-check cadence).
 *
 *   - `name` (required): the row's bold label.
 *   - `hint`: the faint one-line description under the name (omit for none).
 *   - `flush` (default false): drop the top border and tighten the bottom padding
 *     — for a row that sits inside its own bordered disclosure, which already owns
 *     the divider (Library → Advanced).
 *   - `spaced` (default false): add extra top separation — for a row standing
 *     alone after unrelated content (Extensions → update-check cadence).
 *
 * Slot:
 *   - default: the trailing control (a DurationInput, a TextField, a status line).
 */
withDefaults(defineProps<{
  /** The row's bold label. */
  name: string
  /** Faint one-line description under the name. */
  hint?: string
  /** Drop the top border + tighten the bottom (inside a bordered disclosure). */
  flush?: boolean
  /** Add extra top separation (a row standing alone after other content). */
  spaced?: boolean
}>(), {
  flush: false,
  spaced: false,
})
</script>

<template>
  <div class="setting-row" :class="{ 'setting-row--flush': flush, 'setting-row--spaced': spaced }">
    <div class="setting-row__label">
      <div class="setting-row__name">{{ name }}</div>
      <div v-if="hint" class="setting-row__hint">{{ hint }}</div>
    </div>
    <slot />
  </div>
</template>

<style scoped>
.setting-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 13px 0;
  border-top: 1px solid var(--border);
}

/* Inside a bordered disclosure (Library → Advanced): the wrapper owns the
   divider, so the row drops its own border and tightens against what follows. */
.setting-row--flush {
  border-top: none;
  padding-bottom: 2px;
}

/* Standing alone after unrelated content (Extensions → update-check cadence):
   extra top separation. */
.setting-row--spaced {
  margin-top: 16px;
  padding-top: 14px;
}

.setting-row__name {
  font-size: 13.5px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.setting-row__hint {
  font-size: 11.5px;
  color: var(--faint);
}
</style>
