/**
 * useSwUpdate — detects a waiting (updated) service worker and applies it on the
 * owner's click.
 *
 * A deployed new sw.js now parks in `waiting` (public/sw.js no longer calls
 * skipWaiting on install). `watch(registration)` listens for the new worker
 * reaching `installed` WHILE a controller already exists (i.e. this is an UPDATE,
 * not the first install) and flips `updateAvailable`. The frontend surfaces a
 * "New version — Reload" toast; `applyUpdate()` posts SKIP_WAITING to the waiting
 * worker and reloads once it takes control (controllerchange).
 *
 * Module-singleton (like useProgressStream): the plugin calls `watch` after
 * registering, the layout reads `updateAvailable` + calls `applyUpdate` — both
 * share the same reactive ref.
 */
import { ref } from 'vue'

const updateAvailable = ref(false)
let waitingWorker: ServiceWorker | null = null
let reloading = false

export function useSwUpdate() {
  /**
   * watch wires the update-detection listeners onto a registration. Called by
   * the pwa plugin after navigator.serviceWorker.register resolves.
   */
  function watch(reg: ServiceWorkerRegistration): void {
    // A worker may already be waiting (updated before this code ran).
    if (reg.waiting && navigator.serviceWorker.controller) {
      waitingWorker = reg.waiting
      updateAvailable.value = true
    }
    reg.addEventListener('updatefound', () => {
      const installing = reg.installing
      if (!installing) return
      installing.addEventListener('statechange', () => {
        // 'installed' + an existing controller ⇒ an UPDATE parked in waiting
        // (the first install has no prior controller and must not prompt).
        if (installing.state === 'installed' && navigator.serviceWorker.controller) {
          waitingWorker = reg.waiting ?? installing
          updateAvailable.value = true
        }
      })
    })
  }

  /**
   * applyUpdate tells the waiting worker to take over (SKIP_WAITING) and reloads
   * the page once it becomes the controller — so the reload lands on the new
   * assets, exactly once.
   */
  function applyUpdate(): void {
    const worker = waitingWorker
    if (!worker) return
    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (reloading) return
      reloading = true
      window.location.reload()
    })
    worker.postMessage({ type: 'SKIP_WAITING' })
    updateAvailable.value = false
  }

  return { updateAvailable, watch, applyUpdate }
}
