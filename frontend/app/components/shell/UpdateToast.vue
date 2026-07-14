<script setup lang="ts">
import AppButton from '../ui/AppButton.vue'

/**
 * UpdateToast — a floating "New version — Reload" prompt shown when a new service
 * worker is waiting (useSwUpdate.updateAvailable). Presentation-only: the layout
 * owns the state and reacts to `reload`. Renders nothing when no update is
 * available, so it never nags mid-session unless there is genuinely a new build.
 *
 *   - `updateAvailable`: whether a waiting worker is ready (drives visibility).
 *
 * Emits `reload` when the owner accepts (the layout calls applyUpdate).
 */
defineProps<{
  /** Whether a new version is waiting to activate (renders the toast when true). */
  updateAvailable: boolean
}>()

const emit = defineEmits<{
  /** The owner accepted the update — apply it and reload. */
  reload: []
}>()
</script>

<template>
  <div v-if="updateAvailable" class="update" role="status" aria-live="polite">
    <span class="update__text">New version available</span>
    <AppButton variant="primary" size="sm" @click="emit('reload')">Reload</AppButton>
  </div>
</template>

<style scoped>
.update {
  position: fixed;
  left: 50%;
  bottom: 20px;
  transform: translateX(-50%);
  z-index: 65;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 10px 12px 10px 16px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--border);
  background: var(--surface2);
  box-shadow: 0 10px 30px rgb(0 0 0 / 35%);
}

.update__text {
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  color: var(--text);
  white-space: nowrap;
}
</style>
