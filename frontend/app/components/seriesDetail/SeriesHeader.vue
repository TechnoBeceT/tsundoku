<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Chip from '../ui/Chip.vue'
import CoverImage from '../ui/CoverImage.vue'
import SelectField from '../ui/SelectField.vue'
import Toggle from '../ui/Toggle.vue'
import type { SelectOption } from '../ui/forms.types'
import type { SeriesDetail } from '../screens/seriesDetail.types'

/**
 * SeriesHeader — the Series-Detail header card: the cover, the category chip +
 * title, a Delete button, the four chapter-count stat boxes, the Monitored /
 * Completed toggles, and the category select. Presentation-only — the series +
 * the category options arrive via props; every control change is emitted, and the
 * stats reflect whatever counts the parent feeds back (the §16 success round-trip).
 */
const props = defineProps<{
  /** The series to render (header fields + chapter counts). */
  series: SeriesDetail
  /** Category names for the recategorize select (dynamic, user-defined list). */
  categoryOptions: string[]
  /** True while an inline mutation is in flight — disables the controls. */
  saving?: boolean
}>()

const emit = defineEmits<{
  /** The category select changed — carries the new category name. */
  changeCategory: [category: string]
  /** The Monitored toggle flipped — carries the NEW value. */
  toggleMonitored: [monitored: boolean]
  /** The Completed toggle flipped — carries the NEW value. */
  toggleCompleted: [completed: boolean]
  /** The Delete button was pressed (→ the parent opens the delete dialog). */
  requestDelete: []
}>()

const counts = computed(() => props.series.chapterCounts)
// {value,label} pairs for the SelectField (the value IS the category name).
const categorySelectOptions = computed<SelectOption[]>(() =>
  props.categoryOptions.map((c) => ({ value: c, label: c })),
)
</script>

<template>
  <section class="header">
    <div class="header__cover">
      <CoverImage :src="series.coverUrl" :alt="`${series.title} cover`" />
    </div>

    <div class="header__main">
      <div class="header__titlerow">
        <div class="header__titlebox">
          <Chip variant="category">{{ series.category }}</Chip>
          <h1 class="header__title">{{ series.title }}</h1>
        </div>
        <AppButton variant="danger-ghost" size="sm" @click="emit('requestDelete')">
          <template #icon>
            <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
            </svg>
          </template>
          Delete
        </AppButton>
      </div>

      <!-- Chapter-count stats -->
      <div class="stats">
        <div class="stat">
          <div class="stat__label">Total</div>
          <div class="stat__value">{{ counts.total }}</div>
        </div>
        <div class="stat">
          <div class="stat__label">On disk</div>
          <div class="stat__value stat__value--disk">{{ counts.downloaded }}</div>
        </div>
        <div class="stat">
          <div class="stat__label">Wanted</div>
          <div class="stat__value">{{ counts.wanted }}</div>
        </div>
        <div class="stat">
          <div class="stat__label">Failed</div>
          <div class="stat__value stat__value--failed">{{ counts.failed }}</div>
        </div>
      </div>

      <!-- Toggles + category select -->
      <div class="controls">
        <!-- eslint-disable vue/attribute-hyphenation -->
        <!-- camelCase :ariaLabel binds Toggle's REQUIRED prop; kebab :aria-label
             routes to the native attr, leaving it unset (vue-tsc error). -->
        <div class="control">
          <Toggle
            :model-value="series.monitored"
            :disabled="saving"
            :ariaLabel="'Monitored'"
            @update:model-value="emit('toggleMonitored', $event)"
          />
          <div>
            <div class="control__title">Monitored</div>
            <div class="control__hint">Auto-check for new chapters</div>
          </div>
        </div>

        <div class="control">
          <Toggle
            :model-value="series.completed"
            :disabled="saving"
            :ariaLabel="'Completed'"
            @update:model-value="emit('toggleCompleted', $event)"
          />
          <div>
            <div class="control__title">Completed</div>
            <div class="control__hint">Mark finished · skip sweeps</div>
          </div>
        </div>
        <!-- eslint-enable vue/attribute-hyphenation -->

        <label class="control control--category">
          <span class="control__catlabel">Category</span>
          <SelectField
            :model-value="series.category"
            :options="categorySelectOptions"
            :disabled="saving"
            aria-label="Category"
            @update:model-value="emit('changeCategory', $event)"
          />
        </label>
      </div>
    </div>
  </section>
</template>

<style scoped>
.header {
  display: flex;
  gap: 22px;
  flex-wrap: wrap;
  margin-bottom: 18px;
  padding: 20px;
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border);
  background: var(--surface);
}

.header__cover {
  width: 152px;
  flex: none;
  border-radius: var(--radius-lg);
  overflow: hidden;
  border: 1px solid var(--border);
}

.header__main {
  flex: 1;
  min-width: 260px;
  display: flex;
  flex-direction: column;
  gap: 15px;
}

.header__titlerow {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.header__titlebox {
  flex: 1;
  min-width: 0;
}

.header__title {
  margin: 9px 0 0;
  font-family: var(--font-display);
  font-weight: var(--weight-black);
  font-size: var(--text-3xl);
  line-height: 1.12;
  color: var(--text);
}

/* ---- Stats ---------------------------------------------------------------- */
.stats {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}

.stat {
  flex: 1;
  min-width: 88px;
  padding: 11px 13px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.stat__label {
  margin-bottom: 3px;
  font-size: var(--text-xs);
  font-weight: var(--weight-semibold);
  color: var(--faint);
}

.stat__value {
  font-family: var(--font-display);
  font-size: var(--text-2xl);
  font-weight: var(--weight-extrabold);
  color: var(--text);
}

.stat__value--disk {
  color: var(--sd-stat-disk);
}

.stat__value--failed {
  color: var(--danger-bright);
}

/* ---- Controls (toggles + category) ---------------------------------------- */
.controls {
  display: flex;
  align-items: center;
  gap: 24px;
  flex-wrap: wrap;
  margin-top: 2px;
}

.control {
  display: flex;
  align-items: center;
  gap: 11px;
}

.control--category {
  margin-left: auto;
  gap: 9px;
  cursor: pointer;
}

.control__title {
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.control__hint {
  font-size: var(--text-xs);
  color: var(--faint);
}

.control__catlabel {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--faint);
}
</style>
