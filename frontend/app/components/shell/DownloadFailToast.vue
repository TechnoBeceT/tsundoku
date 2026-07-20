<script setup lang="ts">
/**
 * DownloadFailToast — the in-app toast for a `download.fail` SSE event: a
 * fixed bottom-right DANGER card (distinct from the accent `ChapterToast`) naming
 * how many chapters failed and the latest error. Tapping it deep-links to the
 * Downloads → Failed view; the close button dismisses.
 *
 * Presentation-only: the notifier owns aggregation, visibility, and the
 * auto-dismiss timer.
 */
defineProps<{
  /** The headline, e.g. "Download failed" or "3 downloads failed". */
  title: string
  /** The latest failure reason (truncated by CSS). */
  body: string
}>()

const emit = defineEmits<{
  /** The toast body was tapped — open the Failed downloads view. */
  open: []
  /** The close button was pressed. */
  dismiss: []
}>()
</script>

<template>
  <div class="toast" role="alert" aria-live="assertive">
    <button type="button" class="toast__body" @click="emit('open')">
      <span class="toast__icon" aria-hidden="true">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" /><path d="M12 9v4" /><path d="M12 17h.01" /></svg>
      </span>
      <span class="toast__text">
        <span class="toast__title">{{ title }}</span>
        <span class="toast__sub">{{ body }}</span>
      </span>
    </button>
    <button type="button" class="toast__close" aria-label="Dismiss" @click="emit('dismiss')">
      <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 6 6 18" /><path d="m6 6 12 12" /></svg>
    </button>
  </div>
</template>

<style scoped>
.toast {
  position: fixed;
  right: 20px;
  bottom: 20px;
  z-index: 60;
  display: flex;
  align-items: stretch;
  max-width: 340px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--danger-border);
  background: var(--surface2);
  box-shadow: 0 10px 30px rgb(0 0 0 / 35%);
  overflow: hidden;
}

.toast__body {
  display: flex;
  align-items: center;
  gap: 11px;
  flex: 1;
  min-width: 0;
  padding: 12px 6px 12px 13px;
  border: none;
  background: transparent;
  color: var(--text);
  cursor: pointer;
  text-align: left;
}

.toast__icon {
  display: grid;
  place-items: center;
  flex: none;
  width: 32px;
  height: 32px;
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  color: var(--danger-bright);
}

.toast__text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.toast__title {
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  color: var(--danger-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.toast__sub {
  font-size: var(--text-sm);
  color: var(--muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.toast__close {
  display: grid;
  place-items: center;
  flex: none;
  width: 34px;
  border: none;
  border-left: 1px solid var(--border);
  background: transparent;
  color: var(--muted);
  cursor: pointer;
  transition: color 0.15s;
}

.toast__close:hover {
  color: var(--text);
}
</style>
