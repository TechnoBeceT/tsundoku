/**
 * useNotifications — the data + browser-permission layer for the Settings →
 * Notifications pane.
 *
 * It manages TWO independent switches:
 *   1. The GLOBAL toggle (`globalEnabled`) — the server-side
 *      `notifications.enabled` runtime setting (PATCH /api/settings). When off,
 *      the backend notifier never fires on ANY channel for ANY device.
 *   2. This DEVICE's Web Push subscription (`state`) — the browser permission +
 *      push registration. `enable()` requests permission, subscribes via the
 *      PushManager with the server VAPID key, and POSTs the subscription;
 *      `disable()` unsubscribes + DELETEs it.
 *
 * `state` is honest about every platform outcome (§16):
 *   - 'unsupported' — the browser lacks serviceWorker/PushManager/Notification.
 *   - 'blocked'     — the owner denied the permission (must re-enable in browser
 *                     settings; the app cannot re-prompt).
 *   - 'granted'     — subscribed on this device.
 *   - 'default'     — supported + not yet enabled (the Enable button shows).
 *
 * All three states of each async action are surfaced: `busy`/`error` for the
 * device subscribe/unsubscribe, `globalBusy`/`globalError` for the global toggle.
 */
import { ref } from 'vue'
import { apiClient } from '~/utils/api/client'

/** PermissionState is the honest per-device notification status (see composable doc). */
export type PermissionState = 'unsupported' | 'blocked' | 'granted' | 'default'

/** The server settings key backing the global toggle. */
const NOTIFICATIONS_ENABLED_KEY = 'notifications.enabled'

/**
 * urlBase64ToUint8Array converts a base64url VAPID public key into the
 * Uint8Array that pushManager.subscribe expects as applicationServerKey.
 */
export function urlBase64ToUint8Array(base64: string): Uint8Array {
  const padding = '='.repeat((4 - (base64.length % 4)) % 4)
  const normalized = (base64 + padding).replace(/-/g, '+').replace(/_/g, '/')
  const raw = atob(normalized)
  const out = new Uint8Array(raw.length)
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i)
  return out
}

/** pushSupported feature-detects the three APIs Web Push needs. */
function pushSupported(): boolean {
  return typeof navigator !== 'undefined' && 'serviceWorker' in navigator
    && typeof window !== 'undefined' && 'PushManager' in window
    && typeof Notification !== 'undefined'
}

export function useNotifications() {
  const state = ref<PermissionState>('default')
  const globalEnabled = ref(true)

  // §16 state of the per-device enable/disable action.
  const busy = ref(false)
  const error = ref<string | null>(null)

  // §16 state of the global-toggle save.
  const globalBusy = ref(false)
  const globalError = ref<string | null>(null)

  /** Resolve the initial per-device state from the current browser permission. */
  function detectState(): void {
    if (!pushSupported()) {
      state.value = 'unsupported'
      return
    }
    const perm = Notification.permission
    state.value = perm === 'granted' ? 'granted' : perm === 'denied' ? 'blocked' : 'default'
  }

  /** Seed globalEnabled from the server's notifications.enabled setting. */
  async function loadGlobal(): Promise<void> {
    try {
      const res = await apiClient.GET('/api/settings')
      if (res.data) {
        const raw = res.data.find(s => s.key === NOTIFICATIONS_ENABLED_KEY)?.value
        if (raw !== undefined) globalEnabled.value = raw === 'true'
      }
    }
    catch {
      // Leave the optimistic default; the toggle still works via setGlobal.
    }
  }

  /**
   * enable() — request permission, subscribe this device, and register the
   * subscription server-side. Returns true on success. On denial it flips
   * `state` to 'blocked'; any failure lands in `error` (never swallowed, §16).
   */
  async function enable(): Promise<boolean> {
    error.value = null
    if (!pushSupported()) {
      state.value = 'unsupported'
      return false
    }
    busy.value = true
    try {
      const perm = await Notification.requestPermission()
      if (perm === 'denied') {
        state.value = 'blocked'
        return false
      }
      if (perm !== 'granted') {
        state.value = 'default'
        return false
      }
      const keyRes = await apiClient.GET('/api/push/vapid-key')
      if (keyRes.error || !keyRes.data?.key) {
        throw new Error('Could not get the push key from the server')
      }
      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        // Uint8Array is a valid BufferSource at runtime; the DOM lib types want an
        // ArrayBuffer-backed view specifically, so cast (the bytes are correct).
        applicationServerKey: urlBase64ToUint8Array(keyRes.data.key) as BufferSource,
      })
      const json = sub.toJSON()
      if (!json.endpoint || !json.keys?.p256dh || !json.keys?.auth) {
        throw new Error('The browser returned an incomplete subscription')
      }
      const postRes = await apiClient.POST('/api/push/subscriptions', {
        body: { endpoint: json.endpoint, keys: { p256dh: json.keys.p256dh, auth: json.keys.auth } },
      })
      if (postRes.error) throw new Error('Could not register this device for notifications')
      state.value = 'granted'
      return true
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Could not enable notifications'
      return false
    }
    finally {
      busy.value = false
    }
  }

  /**
   * disable() — unsubscribe this device and remove the server-side subscription.
   * Returns true on success; failure lands in `error`.
   */
  async function disable(): Promise<boolean> {
    error.value = null
    if (!pushSupported()) {
      state.value = 'unsupported'
      return false
    }
    busy.value = true
    try {
      const reg = await navigator.serviceWorker.ready
      const sub = await reg.pushManager.getSubscription()
      if (sub) {
        const endpoint = sub.endpoint
        await sub.unsubscribe()
        await apiClient.DELETE('/api/push/subscriptions', { body: { endpoint } })
      }
      state.value = 'default'
      return true
    }
    catch (e) {
      error.value = e instanceof Error ? e.message : 'Could not disable notifications'
      return false
    }
    finally {
      busy.value = false
    }
  }

  /**
   * setGlobal(v) — PATCH the global notifications.enabled setting. Returns true
   * on success; failure lands in `globalError` and leaves globalEnabled unchanged.
   */
  async function setGlobal(v: boolean): Promise<boolean> {
    globalError.value = null
    globalBusy.value = true
    try {
      const res = await apiClient.PATCH('/api/settings', {
        body: { settings: [{ key: NOTIFICATIONS_ENABLED_KEY, value: String(v) }] },
      })
      if (res.error) throw new Error('Could not save the notifications setting')
      globalEnabled.value = v
      return true
    }
    catch (e) {
      globalError.value = e instanceof Error ? e.message : 'Could not save the notifications setting'
      return false
    }
    finally {
      globalBusy.value = false
    }
  }

  detectState()
  void loadGlobal()

  return { state, globalEnabled, busy, error, globalBusy, globalError, enable, disable, setGlobal }
}
