<script setup lang="ts">
import type { NavItem } from '~/components/shell/types'

const route = useRoute()

// Live backend progress stream — connects once on mount, drives shell indicators.
// (The app-global ChapterNotifier in app.vue owns the chapter.new in-app toast.)
const { connect, unhealthyCount, erroringSources, coolingDownSources, syncing } = useProgressStream()
onMounted(connect)

// Persistent download counter (downloading / queued / failed) — polled ~10s and
// reconciled with the live SSE stream, so the rail badge trio is current on EVERY
// page. Replaces the old hardcoded 0/0 rail indicator.
const { downloading, queued, failed } = useDownloadSummary()

// PWA install affordance — captures beforeinstallprompt (Android Chrome) and
// shows the floating "Install app" button until installed.
const { installable, promptInstall } = usePwaInstall()

// Service-worker update prompt — the pwa plugin's watch() flips updateAvailable
// when a new SW parks in waiting; the toast lets the owner reload into it.
const { updateAvailable, applyUpdate } = useSwUpdate()

// Nav items — keys match AppShell's internal references. The 'health' key is
// hardcoded inside AppShell's attention-pill click handler, so it MUST be
// exactly 'health'. Order matches the storybook contract.
const NAV_ITEMS: NavItem[] = [
  { key: 'library', label: 'Library', icon: 'book' },
  { key: 'discover', label: 'Discover', icon: 'compass' },
  { key: 'downloads', label: 'Downloads', icon: 'download' },
  { key: 'health', label: 'Health', icon: 'activity' },
  { key: 'fractionals', label: 'Fractionals', icon: 'scissors' },
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
  fractionals: '/fractionals',
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
  // A nav key may carry a query suffix (e.g. the attention pill emits
  // 'health?tab=library' to force the Library tab); split it off, resolve the
  // base key to its path, and pass the query straight through to navigateTo.
  const [navKey, query] = key.split('?')
  const path = KEY_TO_PATH[navKey ?? '']
  if (path) void navigateTo(query ? `${path}?${query}` : path)
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
    :erroring-sources="erroringSources"
    :cooling-down="coolingDownSources"
    :syncing="syncing"
    :downloading="downloading"
    :queued="queued"
    :failed-downloads="failed"
    @navigate="handleNavigate"
    @toggle-theme="handleToggleTheme"
    @lock="handleLock"
    @open-adopt="handleOpenAdopt"
  >
    <slot />
    <InstallButton :installable="installable" @install="promptInstall" />
    <UpdateToast :update-available="updateAvailable" @reload="applyUpdate" />
  </AppShell>
</template>
