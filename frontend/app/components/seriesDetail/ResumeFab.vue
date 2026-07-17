<script setup lang="ts">
/**
 * ResumeFab — the Komikku-style floating "resume reading" button, pinned to
 * the bottom-right of the Series Detail page. Presentation only: the label
 * ("Start" for a series nobody has opened yet, "Continue" once there's
 * progress) and the target chapter/page are resolved by the caller — this
 * component only renders the button and emits `click`. It must not fetch,
 * navigate, or import a composable; the page decides what happens next.
 */
withDefaults(defineProps<{
  /** Button label — "Start" (never read) or "Continue" (has progress). */
  label: string
  /** Blocks interaction (parity with the app's other action controls). */
  disabled?: boolean
}>(), {
  disabled: false,
})

const emit = defineEmits<{
  /** The FAB was activated — the caller resolves the resume target and navigates. */
  click: []
}>()
</script>

<template>
  <button
    type="button"
    class="resume-fab"
    :disabled="disabled"
    :aria-label="`${label} reading`"
    @click="emit('click')"
  >
    <svg width="15" height="15" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      <path d="M8 5v14l11-7z" />
    </svg>
    <span>{{ label }}</span>
  </button>
</template>

<style scoped>
/* Fixed to the viewport, not an ancestor — floats above the scrolling chapter
 * panel and never competes with its own scrollbar or a row's action buttons
 * (which live inline in the table, never at the viewport edge). z-index sits
 * above the app shell chrome (30) but below dialogs (60/61) — a FAB has no
 * business covering a modal.
 *
 * Offsets are the design's 30px corner inset as rem (30 ÷ 16 = 1.875rem), so the
 * FAB scales with the fluid root. The BOTTOM offset adds `--app-nav-bottom`
 * (base.css: 0px on desktop, 56px + safe-area ≤900px) so on a phone the FAB
 * clears the bottom-nav bar instead of overlapping it; desktop is byte-identical
 * because the token is 0 there. `right` needs no such clearance — the nav rail is
 * on the LEFT (`--app-rail-left`), never the right. The rest of the raw px are
 * migrated to tokens / byte-identical rem literals so the whole button breathes
 * with the root on a phone (§5.16), while staying byte-identical at the 16px
 * desktop anchor. */
.resume-fab {
  position: fixed;
  right: 1.875rem;
  bottom: calc(1.875rem + var(--app-nav-bottom));
  z-index: 45;
  display: inline-flex;
  align-items: center;
  gap: var(--space-xs);
  padding: 0.8125rem 1.375rem 0.8125rem 1.125rem;
  border: none;
  border-radius: var(--radius-pill);
  background: linear-gradient(135deg, var(--accent), var(--accentDeep));
  color: var(--cover-text);
  font-family: var(--font-sans);
  font-size: 0.84375rem;
  font-weight: var(--weight-bold);
  box-shadow: var(--shadow-accent);
  cursor: pointer;
  transition: filter 0.15s, transform 0.15s;
}

.resume-fab:hover:not(:disabled) {
  filter: brightness(1.08);
  transform: translateY(-1px);
}

.resume-fab:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
