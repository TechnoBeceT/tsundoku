/**
 * pwa.client.ts — registers the minimal service worker (public/sw.js) so the app
 * meets the PWA installability bar (installable + fullscreen `standalone`).
 *
 * The manifest is already hand-wired in nuxt.config.ts (`app.head`); this plugin
 * only adds the ONE missing piece — a registered service worker with a fetch
 * handler. It caches nothing (see public/sw.js).
 *
 * Client-only (`.client.ts`) — there is no navigator on the server, and the app
 * is SSR-off anyway. Registration is deferred to `window.load` so it never
 * competes with the initial page/data fetch, and skipped in dev to avoid any
 * interference with Vite HMR (the production build in .output/public is what
 * ships the worker).
 */
export default defineNuxtPlugin(() => {
  if (import.meta.dev) return
  if (!('serviceWorker' in navigator)) return

  const { watch } = useSwUpdate()

  window.addEventListener('load', () => {
    // Best-effort: a registration failure must never break the app — it only
    // means the install prompt is unavailable, which we log for the owner.
    navigator.serviceWorker.register('/sw.js').then((reg) => {
      // Watch for a future deployed SW parking in `waiting` so the layout can
      // surface the "New version — Reload" prompt.
      watch(reg)
    }).catch((err) => {
      console.error('[pwa] service worker registration failed:', err)
    })
  })
})
