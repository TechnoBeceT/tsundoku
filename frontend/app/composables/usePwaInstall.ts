/**
 * usePwaInstall — captures the browser's `beforeinstallprompt` event so the app
 * can offer its OWN "Install app" affordance (Android Chrome). The browser fires
 * that event instead of showing its native mini-infobar; we preventDefault +
 * stash it, expose `installable`, and replay it on the owner's click.
 *
 * Already-installed launches (display-mode: standalone) never show the button —
 * there is nothing to install. iOS is out of scope (owner is Android): Safari
 * fires no `beforeinstallprompt`, so `installable` simply stays false there.
 *
 * Public surface: `{ installable, promptInstall }`.
 */
import { ref, onMounted, onUnmounted } from 'vue'

/**
 * BeforeInstallPromptEvent is the non-standard event Chrome fires; it is not in
 * the DOM lib, so we model the two members we use (prompt + userChoice).
 */
interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>
}

export function usePwaInstall() {
  const installable = ref(false)
  let deferred: BeforeInstallPromptEvent | null = null

  /** True when the app is already running as an installed PWA. */
  function isStandalone(): boolean {
    return typeof window !== 'undefined'
      && typeof window.matchMedia === 'function'
      && window.matchMedia('(display-mode: standalone)').matches
  }

  function onBeforeInstallPrompt(e: Event): void {
    e.preventDefault()
    deferred = e as BeforeInstallPromptEvent
    if (!isStandalone()) installable.value = true
  }

  function onInstalled(): void {
    installable.value = false
    deferred = null
  }

  /**
   * promptInstall replays the stashed prompt (the native install sheet) and
   * clears it — the event is single-use, so the button hides afterward.
   */
  async function promptInstall(): Promise<void> {
    if (!deferred) return
    const e = deferred
    deferred = null
    installable.value = false
    await e.prompt()
  }

  onMounted(() => {
    if (isStandalone()) return
    window.addEventListener('beforeinstallprompt', onBeforeInstallPrompt)
    window.addEventListener('appinstalled', onInstalled)
  })
  onUnmounted(() => {
    window.removeEventListener('beforeinstallprompt', onBeforeInstallPrompt)
    window.removeEventListener('appinstalled', onInstalled)
  })

  return { installable, promptInstall }
}
