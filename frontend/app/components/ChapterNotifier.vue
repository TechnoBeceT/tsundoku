<script setup lang="ts">
import ChapterToast from './shell/ChapterToast.vue'

/**
 * ChapterNotifier — the APP-GLOBAL in-app new-chapter toast. Mounted in app.vue
 * (NOT a layout), so it stays active under EVERY layout — including the reader's
 * `bare` layout. A layout-scoped handler would drop a chapter.new event fired
 * while reading (the service worker suppresses the OS push because the reader is
 * focused, and the watermark has already advanced so it never re-fires).
 *
 * Complementary to the service worker (public/sw.js): the SW suppresses the OS
 * push exactly when a client is FOCUSED, so this shows the in-app toast ONLY when
 * this window is focused (document.hasFocus()) — never both, never neither. A
 * visible-but-unfocused desktop window gets the OS push, not this toast.
 */
interface ChapterNewGroup { seriesId: string, title: string, count: number, url: string }
interface ChapterNewPayload { groups: ChapterNewGroup[], total: number, digest: boolean, title: string, body: string }

// The progress stream is a module singleton; connect() is idempotent, so calling
// it here guarantees delivery even on routes (e.g. the reader) whose layout does
// not itself connect.
const { connect, on } = useProgressStream()
onMounted(connect)

const toast = ref<{ title: string, body: string, url: string } | null>(null)
let toastTimer: ReturnType<typeof setTimeout> | null = null

function dismiss(): void {
  toast.value = null
  if (toastTimer) { clearTimeout(toastTimer); toastTimer = null }
}

function open(): void {
  const url = toast.value?.url ?? '/'
  dismiss()
  void navigateTo(url)
}

let unsubscribe: (() => void) | null = null
onMounted(() => {
  unsubscribe = on('chapter.new', (data) => {
    // Show the in-app toast ONLY when this window is focused (see component doc:
    // strictly complementary to the SW's `focused` suppression).
    if (typeof document !== 'undefined' && !document.hasFocus()) return
    const p = data as ChapterNewPayload
    const url = p.digest ? '/' : (p.groups?.[0]?.url ?? '/')
    toast.value = { title: p.title, body: p.body, url }
    if (toastTimer) clearTimeout(toastTimer)
    toastTimer = setTimeout(dismiss, 6000)
  })
})
onUnmounted(() => {
  unsubscribe?.()
  if (toastTimer) clearTimeout(toastTimer)
})
</script>

<template>
  <ChapterToast
    v-if="toast"
    :title="toast.title"
    :body="toast.body"
    @open="open"
    @dismiss="dismiss"
  />
</template>
