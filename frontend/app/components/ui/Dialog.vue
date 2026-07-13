<script setup lang="ts">
import {
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
  VisuallyHidden,
} from 'reka-ui'
import IconButton from './IconButton.vue'

/**
 * Dialog — the modal overlay + centered card shell, built on reka-ui's accessible
 * `Dialog*` primitives (focus-trap, Escape-to-close, scroll-lock, and
 * overlay-click-close all come from reka for free). We supply only the token CSS
 * for the dim/blur overlay and the card.
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `title`: the header heading — also the accessible name. When omitted, a
 *     visually-hidden fallback name is supplied so the dialog is still labelled.
 *   - `busy`: an in-flight flag — while true, Escape + overlay-click are blocked
 *     so a running mutation can't be interrupted (§16).
 *   - `maxWidth`: the card's max-width (default `480px` — the standard dialog
 *     width, so existing consumers are byte-unchanged). A wider value (e.g.
 *     `800px`) turns the shell into a gallery for grid content.
 *
 * Slots: default (the body) + `actions` (the footer button row).
 * Emits `update:open` (v-model) and `close` (fired whenever it transitions to closed).
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is open (v-model:open). */
  open: boolean
  /** Header heading + accessible name. */
  title?: string
  /** In-flight flag — blocks Escape + overlay-click close. */
  busy?: boolean
  /** The card's max-width (default `480px` — the standard dialog width). */
  maxWidth?: string
}>(), {
  busy: false,
  maxWidth: '480px',
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** The dialog closed (any close path). */
  'close': []
}>()

// reka drives open/closed through one callback; mirror it to v-model and fire
// `close` on the falling edge so consumers can react to dismissal directly.
function onOpenChange(value: boolean) {
  emit('update:open', value)
  if (!value) emit('close')
}

// While busy, swallow the auto-close interactions (Escape / outside-click).
function guardClose(event: Event) {
  if (props.busy) event.preventDefault()
}
</script>

<template>
  <DialogRoot :open="open" @update:open="onOpenChange">
    <DialogPortal>
      <DialogOverlay class="overlay" />
      <DialogContent
        class="dialog"
        :style="{ maxWidth }"
        @escape-key-down="guardClose"
        @pointer-down-outside="guardClose"
        @interact-outside="guardClose"
      >
        <div class="dialog__head">
          <DialogTitle v-if="title" class="dialog__title">{{ title }}</DialogTitle>
          <VisuallyHidden v-else as-child>
            <DialogTitle>Dialog</DialogTitle>
          </VisuallyHidden>
          <DialogClose v-if="!busy" as-child>
            <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
            <IconButton class="dialog__close" :ariaLabel="'Close dialog'">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </IconButton>
          </DialogClose>
        </div>

        <!-- reka requires a Description for full labelling; the body provides it. -->
        <DialogDescription as="div" class="dialog__body">
          <slot />
        </DialogDescription>

        <div v-if="$slots.actions" class="dialog__actions">
          <slot name="actions" />
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: 60;
  background: rgba(5, 4, 9, 0.66);
  backdrop-filter: blur(3px);
}

.dialog {
  position: fixed;
  top: 50%;
  left: 50%;
  z-index: 61;
  transform: translate(-50%, -50%);
  width: calc(100vw - 48px);
  /* max-width is supplied inline from the `maxWidth` prop (default 480px). */
  max-height: calc(100vh - 48px);
  overflow-y: auto;
  padding: 24px;
  border-radius: var(--radius-2xl);
  border: 1px solid var(--border2);
  background: var(--surface);
  box-shadow: var(--shadow);
}

.dialog__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 6px;
}

.dialog__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-xl);
  color: var(--text);
}

.dialog__close {
  flex: none;
}

.dialog__body {
  font-size: var(--text-base);
  line-height: 1.5;
  color: var(--muted);
}

.dialog__actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 22px;
}
</style>
