<script setup lang="ts">
/**
 * FormError — a single inline validation-error line: a small warning icon plus a
 * danger-toned message. This is the NON-dismissible field/form error (e.g. an
 * invalid URL under an input); the dismissible error banner is a separate atom.
 *
 * `message` is the text to show. It carries `role="alert"` so assistive tech
 * announces it when it appears. Token-only colours → both themes work.
 */
defineProps<{
  /** The error message to display. */
  message: string
}>()
</script>

<template>
  <p class="form-error" role="alert">
    <svg class="form-error__icon" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <circle cx="12" cy="12" r="10" />
      <path d="M12 8v4M12 16h.01" />
    </svg>
    <span class="form-error__msg">{{ message }}</span>
  </p>
</template>

<style scoped>
.form-error {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  color: var(--danger-text);
  font-family: var(--font-sans);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  line-height: 1.4;
}

.form-error__icon {
  flex: none;
}

.form-error__msg {
  min-width: 0;
}
</style>
