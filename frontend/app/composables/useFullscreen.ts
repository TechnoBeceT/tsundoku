import { onBeforeUnmount, onMounted, readonly, ref } from 'vue'

/**
 * useFullscreen — a thin, feature-detected wrapper over the browser Fullscreen
 * API for taking one element edge-to-edge (the reader container on a phone).
 *
 * The manifest's `display: standalone` already gives fullscreen once the app is
 * INSTALLED; this composable is the in-browser bonus so the owner can go
 * edge-to-edge before installing.
 *
 * Returns:
 *   - `supported` — whether the API is usable at all (`document.fullscreenEnabled`);
 *     callers hide their toggle when false.
 *   - `isFullscreen` — reactive, kept in sync with `document.fullscreenElement`
 *     (so it also flips when the user leaves fullscreen via Esc / the OS).
 *   - `toggle(el)` — enter fullscreen on `el` when not fullscreen, else exit.
 *
 * Best-effort: a rejected request (user-denied / unavailable) is swallowed — it
 * only means the view stays windowed, never a thrown error. Must be called from
 * a component `setup()` (it registers a `fullscreenchange` listener on mount and
 * removes it on unmount).
 */
export function useFullscreen() {
  const supported = ref(false)
  const isFullscreen = ref(false)

  /** Mirror the live browser fullscreen state onto our reactive flag. */
  function sync(): void {
    isFullscreen.value = Boolean(document.fullscreenElement)
  }

  /** Enter fullscreen on `el`, or exit if already fullscreen. */
  async function toggle(el: HTMLElement): Promise<void> {
    try {
      if (document.fullscreenElement) await document.exitFullscreen()
      else await el.requestFullscreen()
    }
    catch {
      // User-denied or unavailable — stay windowed. `sync` keeps the flag honest.
    }
  }

  onMounted(() => {
    supported.value = Boolean(document.fullscreenEnabled)
    sync()
    document.addEventListener('fullscreenchange', sync)
  })

  onBeforeUnmount(() => {
    document.removeEventListener('fullscreenchange', sync)
  })

  return { supported: readonly(supported), isFullscreen: readonly(isFullscreen), toggle }
}
