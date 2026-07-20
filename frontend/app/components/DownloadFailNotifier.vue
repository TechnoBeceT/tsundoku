<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import DownloadFailToast from './shell/DownloadFailToast.vue'

/**
 * DownloadFailNotifier — the APP-GLOBAL in-app download-failure toast. Mounted in
 * app.vue (like ChapterNotifier) so it stays active under EVERY layout.
 *
 * The backend fires `download.fail` (payload { chapter_id, state, error }) but NO
 * OS/Web-Push notification for a failure, and until now nothing consumed the
 * event. This surfaces it as a THROTTLED, AGGREGATED danger toast so a whole
 * convergence wave of failures shows one "N downloads failed" card, not a storm:
 *   - the first failure shows immediately;
 *   - further failures within the throttle window are COUNTED, then shown as one
 *     aggregated toast when the window elapses (leading + trailing).
 * Tapping the toast deep-links to Downloads → Failed (the list has already been
 * refetched by useDownloads' own `download.fail` listener).
 *
 * Only shown while the tab is visible — a hidden tab has no one to see it, and the
 * Downloads list is refetched regardless when it next mounts.
 */
interface DownloadFailPayload { chapter_id?: string, state?: string, error?: string }

const THROTTLE_MS = 8000
const AUTODISMISS_MS = 7000

const { connect, on } = useProgressStream()
onMounted(connect)

const toast = ref<{ title: string, body: string } | null>(null)

// Aggregation state: failures counted since the last toast, plus the latest error.
let bucket = 0
let latestError = ''
let inCooldown = false
let cooldownTimer: ReturnType<typeof setTimeout> | null = null
let dismissTimer: ReturnType<typeof setTimeout> | null = null

function clearTimers(): void {
  if (cooldownTimer) { clearTimeout(cooldownTimer); cooldownTimer = null }
  if (dismissTimer) { clearTimeout(dismissTimer); dismissTimer = null }
}

function dismiss(): void {
  toast.value = null
  if (dismissTimer) { clearTimeout(dismissTimer); dismissTimer = null }
}

function open(): void {
  dismiss()
  void navigateTo('/downloads')
}

// Render the aggregated toast for everything counted since the last one, then open
// the throttle window; if more failures land during it, show once more on close.
function showToast(): void {
  const count = bucket
  bucket = 0
  if (count === 0) return
  toast.value = {
    title: count === 1 ? 'Download failed' : `${count} downloads failed`,
    body: latestError || 'A chapter could not be downloaded.',
  }
  if (dismissTimer) clearTimeout(dismissTimer)
  dismissTimer = setTimeout(dismiss, AUTODISMISS_MS)

  inCooldown = true
  cooldownTimer = setTimeout(() => {
    inCooldown = false
    cooldownTimer = null
    // Trailing edge: fold in any failures that arrived during the window.
    if (bucket > 0) showToast()
  }, THROTTLE_MS)
}

let unsubscribe: (() => void) | null = null
onMounted(() => {
  unsubscribe = on('download.fail', (data) => {
    // No one to see it on a hidden tab; the list is refetched when it next mounts.
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
    const p = data as DownloadFailPayload
    bucket++
    if (p.error) latestError = p.error
    // Leading edge: show immediately unless a toast window is already open.
    if (!inCooldown) showToast()
  })
})

onUnmounted(() => {
  unsubscribe?.()
  clearTimers()
})
</script>

<template>
  <DownloadFailToast
    v-if="toast"
    :title="toast.title"
    :body="toast.body"
    @open="open"
    @dismiss="dismiss"
  />
</template>
