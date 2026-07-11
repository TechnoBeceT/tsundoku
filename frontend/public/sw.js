/*
 * sw.js — Tsundoku's minimal service worker.
 *
 * PURPOSE: satisfy the PWA installability bar so the app can be installed to a
 * phone/tablet/desktop home screen and launch fullscreen (manifest
 * `display: standalone`). Browsers gate the install prompt on a registered
 * service worker that has a `fetch` handler — this file provides exactly that
 * and NOTHING more.
 *
 * DELIBERATE NON-GOAL — no offline / no caching (v1 scope, owner-ratified):
 *   - It caches NO pages, NO app-shell bytes, and NO `/api/**` responses. API
 *     data is owner-private, and page-image bytes (`/api/.../pages/*`) are
 *     `Cache-Control: private, max-age=300` and can change on a convergence
 *     upgrade — persisting them in a SW cache would serve stale/leaked data.
 *   - The fetch handler is a transparent network pass-through: identical bytes,
 *     no storage. Offline behaviour is therefore unchanged from having no SW.
 *
 * If offline support is ever wanted, add a Workbox/precache strategy here — but
 * keep `/api/**` on NetworkOnly regardless.
 */

// Activate a new worker immediately instead of waiting for all tabs to close,
// so an updated SW (and any future logic) takes effect on the next load.
self.addEventListener('install', () => {
  self.skipWaiting()
})

// Take control of already-open clients as soon as this worker activates.
self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

// Transparent network pass-through for GET requests. Registering this handler is
// what makes the app installable; we intentionally store nothing. Non-GET
// requests (POST/PATCH/DELETE mutations) are left to the browser's default path.
self.addEventListener('fetch', (event) => {
  if (event.request.method !== 'GET') return
  event.respondWith(fetch(event.request))
})
