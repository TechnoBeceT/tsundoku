<script setup lang="ts">
import { computed } from 'vue'

/**
 * ActiveFailureBanner — the Active-tab "not idle, WAITING" banner.
 *
 * When the Active list is empty it is tempting to read "up to date" — but the
 * queue can be empty of ACTIVE work while chapters are FAILING or their sources
 * are in anti-ban cooldown. This banner states that truth instead of a misleading
 * "all caught up": "N chapters failing · M sources cooling down", each half a
 * link (failing → the Failed tab; cooling down → the source-health view).
 *
 * The parent renders it only when there is something to say (failing > 0 OR
 * coolingDown > 0); each half hides independently at 0. Presentation only —
 * two intents out, no state.
 */
const props = withDefaults(defineProps<{
  /** Chapters in a failed/terminal state right now (exact server count). */
  failing?: number
  /** Sources whose circuit-breaker is tripped (anti-ban cooldown) right now. */
  coolingDown?: number
}>(), {
  failing: 0,
  coolingDown: 0,
})

const emit = defineEmits<{
  /** "N chapters failing" was clicked — open the Failed tab. */
  'view-failed': []
  /** "M sources cooling down" was clicked — open the source-health view. */
  'view-sources': []
}>()

const hasFailing = computed(() => props.failing > 0)
const hasCooling = computed(() => props.coolingDown > 0)
</script>

<template>
  <div class="banner" role="status">
    <svg class="banner__icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    </svg>
    <span class="banner__text">
      <button v-if="hasFailing" type="button" class="banner__link banner__link--failed" @click="emit('view-failed')">
        {{ failing }} {{ failing === 1 ? 'chapter' : 'chapters' }} failing
      </button>
      <span v-if="hasFailing && hasCooling" class="banner__sep" aria-hidden="true">·</span>
      <button v-if="hasCooling" type="button" class="banner__link banner__link--cooling" @click="emit('view-sources')">
        {{ coolingDown }} {{ coolingDown === 1 ? 'source' : 'sources' }} cooling down
      </button>
    </span>
  </div>
</template>

<style scoped>
.banner {
  display: flex;
  align-items: center;
  gap: var(--space-sm);
  padding: var(--space-sm) var(--space-base);
  border-radius: var(--radius-lg);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  color: var(--danger-text);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  margin-bottom: var(--space-base);
}

.banner__icon {
  flex: none;
  color: var(--danger-bright);
}

.banner__text {
  display: flex;
  align-items: center;
  gap: var(--space-xs);
  flex-wrap: wrap;
}

.banner__link {
  padding: 0;
  border: none;
  background: none;
  cursor: pointer;
  font: inherit;
  color: var(--danger-text);
  text-decoration: underline;
  text-underline-offset: 2px;
}

.banner__link--cooling {
  color: var(--warn);
}

.banner__link:hover {
  filter: brightness(1.12);
}

.banner__link:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
  border-radius: var(--radius-xs);
}

.banner__sep {
  color: var(--faint);
}
</style>
