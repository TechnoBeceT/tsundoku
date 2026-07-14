<script setup lang="ts">
/**
 * ChapterToast — the in-app toast for a `chapter.new` SSE event (shown only while
 * the tab is visible; the service worker shows the OS notification otherwise). A
 * fixed bottom-right card with the pre-rendered title/body from the payload.
 * Presentation-only: the layout owns visibility + the auto-dismiss timer.
 *
 *   - `title` / `body`: the pre-rendered notification text from the payload.
 *
 * Tapping the card emits `open` (the layout deep-links to the series/library);
 * the close button emits `dismiss`.
 */
defineProps<{
  /** The notification title (series name or "New chapters"). */
  title: string
  /** The notification body (chapter count / cross-series summary). */
  body: string
}>()

const emit = defineEmits<{
  /** The toast body was tapped — open the deep-link. */
  open: []
  /** The close button was pressed. */
  dismiss: []
}>()
</script>

<template>
  <div class="toast" role="status" aria-live="polite">
    <button type="button" class="toast__body" @click="emit('open')">
      <span class="toast__icon" aria-hidden="true">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" /><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" /></svg>
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
  border: 1px solid var(--border);
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
  background: var(--accentSoft);
  color: var(--accent);
}

.toast__text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.toast__title {
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  color: var(--text);
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
