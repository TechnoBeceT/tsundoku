<script setup lang="ts">
import AppButton from '../ui/AppButton.vue'
import FormError from '../ui/FormError.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import Toggle from '../ui/Toggle.vue'

/**
 * NotificationsPane — the Notifications settings pane. Two switches:
 *   1. The GLOBAL toggle (server-side notifications.enabled): when off, the
 *      backend notifier fires on NO channel for ANY device.
 *   2. This DEVICE's Web Push subscription, with honest per-platform states
 *      (granted / blocked / unsupported / default).
 *
 * Presentation-only: ALL state arrives via props and every action is emitted —
 * the page owns the data (useNotifications). The §16 trio is visible for BOTH
 * actions: `globalBusy`/`globalError` for the toggle, `busy`/`error` for the
 * per-device enable/disable.
 *
 *   - `state`: this device's push status (granted/blocked/unsupported/default).
 *   - `globalEnabled`: the server-side global toggle value.
 *   - `busy` / `error`: the per-device enable/disable action's in-flight/failure.
 *   - `globalBusy` / `globalError`: the global toggle save's in-flight/failure.
 */
withDefaults(defineProps<{
  /** This device's push status. */
  state: 'unsupported' | 'blocked' | 'granted' | 'default'
  /** The server-side global notifications toggle. */
  globalEnabled: boolean
  /** Whether the per-device enable/disable action is in flight. */
  busy?: boolean
  /** A per-device action failure, surfaced inline. */
  error?: string | null
  /** Whether the global-toggle save is in flight. */
  globalBusy?: boolean
  /** A global-toggle save failure, surfaced inline. */
  globalError?: string | null
}>(), {
  busy: false,
  error: null,
  globalBusy: false,
  globalError: null,
})

const emit = defineEmits<{
  /** Enable Web Push on this device. */
  enable: []
  /** Disable Web Push on this device. */
  disable: []
  /** Flip the global notifications toggle — carries the new value. */
  'set-global': [value: boolean]
}>()
</script>

<template>
  <SurfaceCard
    title="Notifications"
    sub="Get alerted when a followed series gets a new readable chapter — in-app while open, and via Web Push when closed."
  >
    <!-- Global on/off (server-side notifications.enabled). -->
    <div class="row">
      <div class="row__text">
        <p class="row__label">New-chapter notifications</p>
        <p class="row__hint">The master switch. When off, no device is notified.</p>
      </div>
      <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
      <Toggle :model-value="globalEnabled" :ariaLabel="'Toggle new-chapter notifications'" :disabled="globalBusy" @update:model-value="emit('set-global', $event)" />
    </div>
    <div v-if="globalError" class="row-error">
      <FormError :message="globalError" />
    </div>

    <hr class="divider">

    <!-- Per-device Web Push subscription, with honest per-platform states. -->
    <div class="row">
      <div class="row__text">
        <p class="row__label">This device</p>
        <p v-if="state === 'granted'" class="row__hint">Web Push is on for this device.</p>
        <p v-else-if="state === 'blocked'" class="row__hint">
          Notifications are blocked — re-enable them in your browser's site settings.
        </p>
        <p v-else-if="state === 'unsupported'" class="row__hint">
          Web Push isn't supported on this browser.
        </p>
        <p v-else class="row__hint">Turn on push notifications for this device.</p>
      </div>

      <AppButton
        v-if="state === 'granted'"
        variant="mini"
        size="sm"
        :loading="busy"
        @click="emit('disable')"
      >
        Disable
      </AppButton>
      <AppButton
        v-else-if="state === 'default'"
        variant="mini"
        size="sm"
        :loading="busy"
        @click="emit('enable')"
      >
        Enable
      </AppButton>
    </div>
    <div v-if="error" class="row-error">
      <FormError :message="error" />
    </div>
  </SurfaceCard>
</template>

<style scoped>
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 4px 0;
}

.row__text {
  min-width: 0;
}

.row__label {
  margin: 0;
  font-size: var(--text-base);
  font-weight: var(--weight-semibold);
  color: var(--text);
}

.row__hint {
  margin: 3px 0 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

.row-error {
  margin-top: 8px;
}

.divider {
  margin: 14px 0;
  border: none;
  border-top: 1px solid var(--border);
}
</style>
