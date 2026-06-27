<script setup lang="ts">
import IconButton from './IconButton.vue'

/**
 * ErrorBanner — a dismissible danger-toned alert row: an alert icon, the
 * `message`, and (when `dismissible`) a close button. This is the DISMISSIBLE
 * banner for a failed operation (§16 error state); the non-dismissible inline
 * field error is the separate `FormError` atom.
 *
 *   - `message`: the error text to show.
 *   - `dismissible` (default true): render the close (×) button.
 *
 * Carries `role="alert"` so assistive tech announces it on appearance.
 * Emits `dismiss` when the close button is clicked.
 */
withDefaults(defineProps<{
  /** The error message to display. */
  message: string
  /** Whether to show the close button. */
  dismissible?: boolean
}>(), {
  dismissible: true,
})

const emit = defineEmits<{
  /** The owner dismissed the banner. */
  dismiss: []
}>()
</script>

<template>
  <div class="banner" role="alert">
    <svg class="banner__icon" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="10" />
      <path d="M12 8v4M12 16h.01" />
    </svg>
    <span class="banner__msg">{{ message }}</span>
    <IconButton
      v-if="dismissible"
      class="banner__close"
      variant="danger"
      size="sm"
      aria-label="Dismiss error"
      @click="emit('dismiss')"
    >
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M18 6 6 18M6 6l12 12" />
      </svg>
    </IconButton>
  </div>
</template>

<style scoped>
.banner {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 11px 12px;
  border-radius: var(--radius-md);
  border: 1px solid var(--danger-border);
  background: var(--danger-bg);
  color: var(--danger-text);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  line-height: 1.4;
}

.banner__icon {
  flex: none;
}

.banner__msg {
  flex: 1;
  min-width: 0;
}

.banner__close {
  flex: none;
}
</style>
