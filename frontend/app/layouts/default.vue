<script setup lang="ts">
import type { NavItem } from '~/components/shell/types'

const route = useRoute()

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
    :unhealthy="0"
    :syncing="false"
    :active-downloads="0"
    :failed-downloads="0"
    @navigate="handleNavigate"
    @toggle-theme="handleToggleTheme"
    @lock="handleLock"
    @open-adopt="handleOpenAdopt"
  >
    <!-- TODO(task-12): wire :unhealthy, :syncing, :active-downloads, :failed-downloads from useProgressStream -->
    <slot />
  </AppShell>
</template>
