<script setup lang="ts">
import AppButton from '../ui/AppButton.vue'

/**
 * InstallButton — a floating "Install app" affordance shown only when the
 * browser has offered an install prompt (Android Chrome). Presentation-only: the
 * layout owns the `installable` state (usePwaInstall) and reacts to `install`.
 * Renders nothing when not installable, so it is invisible on already-installed
 * launches and on browsers that never fire beforeinstallprompt.
 *
 *   - `installable`: whether an install prompt is available (drives visibility).
 *
 * Emits `install` when tapped (the layout replays the stashed prompt).
 */
defineProps<{
  /** Whether an install prompt is available (renders the button when true). */
  installable: boolean
}>()

const emit = defineEmits<{
  /** The button was tapped — replay the native install prompt. */
  install: []
}>()
</script>

<template>
  <div v-if="installable" class="install">
    <AppButton variant="primary" size="sm" @click="emit('install')">
      <template #icon>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" /><path d="M7 10l5 5 5-5" /><path d="M12 15V3" /></svg>
      </template>
      Install app
    </AppButton>
  </div>
</template>

<style scoped>
.install {
  position: fixed;
  left: 20px;
  bottom: 20px;
  z-index: 55;
}
</style>
