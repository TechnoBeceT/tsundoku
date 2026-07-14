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

/*
 * WEB PUSH — new-readable-chapter notifications.
 *
 * The backend (internal/notify) sends the SAME payload over BOTH channels: an
 * SSE `chapter.new` event for open clients AND Web Push for closed ones. To
 * avoid showing it twice, this handler SUPPRESSES the OS notification when a
 * Tsundoku window is focused — that window already renders the in-app toast from
 * the SSE event. App closed/backgrounded → OS notification; app focused → toast.
 *
 * The payload is the notify.NewChapterNotification shape:
 *   { groups:[{seriesId,title,count,url}], total, digest, title, body }
 * `tag` collapses repeat pushes: a digest replaces the previous digest; a
 * per-series push replaces the previous one for that series, so notifications
 * never stack up.
 */
self.addEventListener('push', (event) => {
  event.waitUntil(handlePush(event))
})

async function handlePush(event) {
  // Focus-suppression: if any Tsundoku window is focused, the in-app toast is
  // already showing this — skip the OS notification.
  const clientList = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
  if (clientList.some((c) => c.focused)) return

  let payload
  try {
    payload = event.data ? event.data.json() : null
  } catch {
    payload = null
  }
  if (!payload) return

  const firstGroup = Array.isArray(payload.groups) ? payload.groups[0] : undefined
  const tag = payload.digest ? 'tsundoku-digest' : `tsundoku-${firstGroup ? firstGroup.seriesId : 'new'}`
  const url = payload.digest ? '/' : firstGroup ? firstGroup.url : '/'

  await self.registration.showNotification(payload.title || 'New chapters', {
    body: payload.body || '',
    tag,
    data: { url },
    icon: '/icon-192.png',
    badge: '/icon-192.png',
  })
}

/*
 * NOTIFICATION CLICK — deep-link into the app. Focus an already-open Tsundoku
 * window (and navigate it to the deep-link) if one exists, else open a new one.
 */
self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  event.waitUntil(handleNotificationClick(event))
})

async function handleNotificationClick(event) {
  const url = (event.notification.data && event.notification.data.url) || '/'
  const clientList = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
  const existing = clientList[0]
  if (existing) {
    await existing.focus()
    if ('navigate' in existing) await existing.navigate(url)
    return
  }
  await self.clients.openWindow(url)
}
