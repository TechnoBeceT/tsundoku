<script setup lang="ts">
import { computed } from 'vue'
import CoverImage from '../ui/CoverImage.vue'
import Toggle from '../ui/Toggle.vue'
import AppButton from '../ui/AppButton.vue'
import type { SeriesFractionals } from '../screens/fractionals.types'

/**
 * FractionalSeriesCard — one series on the library Fractionals page. Everything
 * the owner does to fix a series happens INLINE here:
 *   - a clickable header (cover · title · category) → `open-series` (jump to detail);
 *   - two count badges: total downloaded fractionals and how many are removable now;
 *   - the whole-series "Ignore fractional chapters" Toggle (`toggle-ignore`);
 *   - a "Clean files" button (`clean-files`) that opens the reused cleanup dialog.
 *
 * Presentation-only: the series arrives via props, every action is emitted, and
 * the parent owns the fetch/dialog. `busy` dims the toggle while the whole-series
 * policy write is in flight (§16). Token-only colours → both themes render.
 */
const props = defineProps<{
  /** The series row to render. */
  series: SeriesFractionals
  /** The whole-series ignore-policy write is in flight — dims + blocks the toggle. */
  busy?: boolean
}>()

const emit = defineEmits<{
  /** The header was clicked — open this series' detail view. */
  'open-series': [seriesId: string]
  /** The ignore toggle flipped — set the whole-series policy to `ignore`. */
  'toggle-ignore': [payload: { seriesId: string, ignore: boolean }]
  /** "Clean files" clicked — the parent opens the cleanup dialog for this series. */
  'clean-files': [seriesId: string]
}>()

// "N of M sources ignoring" — the toggle's supporting caption.
const ignoreCaption = computed(() =>
  `${props.series.providersIgnoring} of ${props.series.providersTotal} source${props.series.providersTotal === 1 ? '' : 's'} ignoring`,
)

// Nothing is removable until policy is set — the button would be a dead control.
const canClean = computed(() => props.series.removableCount > 0)
</script>

<template>
  <div class="fcard">
    <button
      type="button"
      class="fcard__head"
      :aria-label="`Open ${series.displayName}`"
      @click="emit('open-series', series.seriesId)"
    >
      <span class="fcard__cover">
        <CoverImage :src="series.coverUrl" :alt="series.displayName" placeholder="initial" aspect="0.777" />
      </span>
      <span class="fcard__titles">
        <span class="fcard__title">{{ series.displayName }}</span>
        <span class="fcard__cat">{{ series.category }}</span>
      </span>
      <svg class="fcard__chevron" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M9 18l6-6-6-6" />
      </svg>
    </button>

    <!-- The two counts, side by side: total junk vs cleanable-right-now. -->
    <div class="fcard__counts">
      <span class="fcount">
        <span class="fcount__value">{{ series.fractionalCount }}</span>
        <span class="fcount__label">fractional{{ series.fractionalCount === 1 ? '' : 's' }}</span>
      </span>
      <span class="fcount fcount--removable">
        <span class="fcount__value">{{ series.removableCount }}</span>
        <span class="fcount__label">removable now</span>
      </span>
    </div>

    <!-- Whole-series ignore policy + clean action. -->
    <div class="fcard__actions">
      <div class="fcard__policy">
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase ariaLabel binds the REQUIRED prop; kebab aria-label routes to the native attr, leaving the prop unset (vue-tsc error). Same footgun as Checkbox in FractionalCleanupDialog. -->
        <Toggle :model-value="series.allProvidersIgnoring" :disabled="busy" ariaLabel="Ignore fractional chapters for every source" @update:model-value="emit('toggle-ignore', { seriesId: series.seriesId, ignore: $event })" />
        <span class="fcard__policy-text">
          <span class="fcard__policy-title">Ignore fractional chapters</span>
          <span class="fcard__policy-sub">{{ ignoreCaption }}</span>
        </span>
      </div>
      <AppButton
        variant="danger-ghost"
        size="sm"
        :disabled="!canClean"
        @click="emit('clean-files', series.seriesId)"
      >
        Clean files
      </AppButton>
    </div>
  </div>
</template>

<style scoped>
.fcard {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-xl);
  padding: var(--space-lg);
  display: flex;
  flex-direction: column;
  gap: var(--space-base);
}

.fcard__head {
  display: flex;
  align-items: center;
  gap: 0.8125rem;
  width: 100%;
  padding: 0;
  border: none;
  background: none;
  text-align: left;
  cursor: pointer;
}

.fcard__cover {
  width: 2.625rem;
  border-radius: var(--radius-sm);
  overflow: hidden;
  flex: none;
}

.fcard__titles {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs);
}

.fcard__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.fcard__cat {
  font-size: var(--text-xs);
  color: var(--faint);
}

.fcard__chevron {
  flex: none;
  color: var(--faint);
}

/* ---- Counts --------------------------------------------------------------- */
.fcard__counts {
  display: flex;
  gap: var(--space-sm);
}

.fcount {
  display: flex;
  align-items: baseline;
  gap: var(--space-2xs);
  padding: var(--space-2xs) var(--space-sm);
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  background: var(--surface2);
}

.fcount--removable {
  border-color: var(--danger-border);
  background: var(--danger-bg);
}

.fcount__value {
  font-size: var(--text-md);
  font-weight: var(--weight-extrabold);
  color: var(--text);
}

.fcount--removable .fcount__value {
  color: var(--danger-text);
}

.fcount__label {
  font-size: var(--text-xs);
  color: var(--muted);
}

/* ---- Actions -------------------------------------------------------------- */
.fcard__actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-base);
  flex-wrap: wrap;
}

.fcard__policy {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  min-width: 0;
}

.fcard__policy-text {
  display: flex;
  flex-direction: column;
  gap: var(--space-3xs);
  min-width: 0;
}

.fcard__policy-title {
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  color: var(--text);
}

.fcard__policy-sub {
  font-size: var(--text-xs);
  color: var(--faint);
}
</style>
