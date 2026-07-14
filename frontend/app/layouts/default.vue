<script setup lang="ts">
import type { NavItem } from '~/components/shell/types'

const route = useRoute()

// Live backend progress stream — connects once on mount, drives shell indicators.
const { connect, unhealthyCount, syncing, on } = useProgressStream()
onMounted(connect)

// PWA install affordance — captures beforeinstallprompt (Android Chrome) and
// shows the floating "Install app" button until installed.
const { installable, promptInstall } = usePwaInstall()

// Service-worker update prompt — the pwa plugin's watch() flips updateAvailable
// when a new SW parks in waiting; the toast lets the owner reload into it.
const { updateAvailable, applyUpdate } = useSwUpdate()

/**
 * In-app new-chapter toast (chapter.new SSE). Shown ONLY while the tab is
 * visible — when it is hidden/closed the service worker's Web Push handler shows
 * the OS notification instead, so the owner never gets both. Tapping the toast
 * deep-links to the series (or the library for a digest); it also auto-dismisses.
 */
interface ChapterNewGroup { seriesId: string, title: string, count: number, url: string }
interface ChapterNewPayload { groups: ChapterNewGroup[], total: number, digest: boolean, title: string, body: string }

const chapterToast = ref<{ title: string, body: string, url: string } | null>(null)
let toastTimer: ReturnType<typeof setTimeout> | null = null

function dismissToast(): void {
  chapterToast.value = null
  if (toastTimer) { clearTimeout(toastTimer); toastTimer = null }
}

function openToast(): void {
  const url = chapterToast.value?.url ?? '/'
  dismissToast()
  void navigateTo(url)
}

let unsubscribeChapterNew: (() => void) | null = null
onMounted(() => {
  unsubscribeChapterNew = on('chapter.new', (data) => {
    if (typeof document !== 'undefined' && document.visibilityState !== 'visible') return
    const p = data as ChapterNewPayload
    const url = p.digest ? '/' : (p.groups?.[0]?.url ?? '/')
    chapterToast.value = { title: p.title, body: p.body, url }
    if (toastTimer) clearTimeout(toastTimer)
    toastTimer = setTimeout(dismissToast, 6000)
  })
})
onUnmounted(() => {
  unsubscribeChapterNew?.()
  if (toastTimer) clearTimeout(toastTimer)
})

// Nav items — keys match AppShell's internal references. The 'health' key is
// hardcoded inside AppShell's attention-pill click handler, so it MUST be
// exactly 'health'. Order matches the storybook contract.
const NAV_ITEMS: NavItem[] = [
  { key: 'library', label: 'Library', icon: 'book' },
  { key: 'discover', label: 'Discover', icon: 'compass' },
  { key: 'downloads', label: 'Downloads', icon: 'download' },
  { key: 'health', label: 'Library Health', icon: 'activity' },
  { key: 'categories', label: 'Categories', icon: 'layout-grid' },
  { key: 'import', label: 'Import', icon: 'file-plus' },
  { key: 'scan-library', label: 'Scan Library', icon: 'folder-search' },
  { key: 'settings', label: 'Settings', icon: 'settings', pinned: true },
]

// Navigation: nav key ↔ route path. NavItem has no path field — routing concerns
// are kept separate from AppShell's presentational nav model.
const KEY_TO_PATH: Record<string, string> = {
  library: '/',
  discover: '/discover',
  downloads: '/downloads',
  health: '/health',
  categories: '/categories',
  import: '/import',
  'scan-library': '/scan-library',
  settings: '/settings',
}

const PATH_TO_KEY: Record<string, string> = Object.fromEntries(
  Object.entries(KEY_TO_PATH).map(([k, p]) => [p, k]),
)

// Resolve which nav key corresponds to the current route path.
const activeRoute = computed(() => PATH_TO_KEY[route.path] ?? '')

// AppShell requires a header title — derive it from the active nav item's label.
const activeItem = computed(() => NAV_ITEMS.find((i) => i.key === activeRoute.value))
const headerTitle = computed(() => activeItem.value?.label ?? 'Tsundoku')

// Theme — read from @nuxtjs/color-mode (writes data-theme="dark|light" on <html>
// per nuxt.config.ts colorMode settings). AppShell only reads theme as a prop;
// the layout owns theme state.
const colorMode = useColorMode()
const theme = computed<'dark' | 'light'>(() =>
  colorMode.value === 'light' ? 'light' : 'dark',
)

function handleNavigate(key: string): void {
  const path = KEY_TO_PATH[key]
  if (path) void navigateTo(path)
}

function handleToggleTheme(): void {
  // Toggle based on the effective value so 'system' preference resolves correctly.
  colorMode.preference = colorMode.value === 'light' ? 'dark' : 'light'
}

function handleLock(): void {
  void navigateTo('/login')
}

function handleOpenAdopt(): void {
  // The header "Adopt series" button opens the Import screen.
  void navigateTo('/import')
}
</script>

<template>
  <AppShell
    :nav-items="NAV_ITEMS"
    :active-route="activeRoute"
    :theme="theme"
    :header-title="headerTitle"
    :unhealthy="unhealthyCount"
    :syncing="syncing"
    :active-downloads="0"
    :failed-downloads="0"
    @navigate="handleNavigate"
    @toggle-theme="handleToggleTheme"
    @lock="handleLock"
    @open-adopt="handleOpenAdopt"
  >
    <!-- active-downloads / failed-downloads stay 0: download.* events carry no running
         total in their payload, so a reliable per-event count cannot be maintained here.
         Authoritative counts come from the Downloads screen (Milestone B). -->
    <slot />
    <ChapterToast
      v-if="chapterToast"
      :title="chapterToast.title"
      :body="chapterToast.body"
      @open="openToast"
      @dismiss="dismissToast"
    />
    <InstallButton :installable="installable" @install="promptInstall" />
    <UpdateToast :update-available="updateAvailable" @reload="applyUpdate" />
  </AppShell>
</template>
