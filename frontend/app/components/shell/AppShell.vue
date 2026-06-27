<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import type { NavItem } from './types'

/**
 * AppShell — the presentational chrome wrapping every main screen: the left nav
 * rail (brand mark, primary nav, bottom-pinned controls) and the top header
 * (title/breadcrumb, sync + "need attention" indicators, Adopt button). Screen
 * content goes in the default `<slot/>`.
 *
 * Presentation only: it owns NO state and fetches nothing. It renders WHATEVER
 * `navItems` it is given (never hardcoding the nav or branching on a role) and
 * surfaces every action as an emit. Theme is read-only here — the shell picks
 * the toggle glyph from `theme` but never holds theme state; the parent owns it.
 * It reads only design tokens, so it renders correctly in both themes.
 */
const props = withDefaults(defineProps<{
  /** The nav rail items, in render order (caller-owned; see `NavItem`). */
  navItems: NavItem[]
  /** Key of the currently-active nav item — highlighted + `aria-current`. */
  activeRoute: string
  /** Current theme — READ ONLY, used to pick the sun/moon toggle glyph. */
  theme: 'dark' | 'light'
  /** The header title (e.g. the screen name or current series title). */
  headerTitle: string
  /** Optional parent crumb shown above the title (e.g. "Library" on a detail). */
  breadcrumb?: string
  /** Count of sources needing attention — drives the header pill (hidden at 0). */
  unhealthy?: number
  /** Whether a sync/download cycle is active — shows the header spinner. */
  syncing?: boolean
  /** Label beside the sync spinner (e.g. "Syncing sources…"). */
  syncLabel?: string
  /** Active downloads — shown as the accent rail-bottom indicator (hidden at 0). */
  activeDownloads?: number
  /** Failed downloads — shown as the amber rail-bottom indicator (hidden at 0). */
  failedDownloads?: number
  /** Whether a mutation is in flight — shows the header's indeterminate bar. */
  mutating?: boolean
}>(), {
  breadcrumb: '',
  unhealthy: 0,
  syncing: false,
  syncLabel: '',
  activeDownloads: 0,
  failedDownloads: 0,
  mutating: false,
})

const emit = defineEmits<{
  /** A nav item (or the brand / breadcrumb) was activated — carries its key. */
  navigate: [key: string]
  /** The theme toggle was pressed — the parent flips the theme. */
  'toggle-theme': []
  /** The lock/logout control was pressed. */
  lock: []
  /** The header "Adopt series" button was pressed. */
  'open-adopt': []
}>()

// Top-flow nav vs the bottom-pinned group (e.g. Settings). The caller marks an
// item `pinned` rather than the shell knowing any item by name.
const topItems = computed(() => props.navItems.filter((i) => !i.pinned))
const pinnedItems = computed(() => props.navItems.filter((i) => i.pinned))

// "Home" = the first non-pinned item; the brand tile + breadcrumb return here.
const homeKey = computed(() => (topItems.value[0] ?? props.navItems[0])?.key ?? '')

// Sun in dark mode (press → go light), moon in light mode. The shell only READS
// theme; it never stores it.
const themeIcon = computed(() => (props.theme === 'dark' ? 'lucide:sun' : 'lucide:moon'))
const themeLabel = computed(() => (props.theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'))
</script>

<template>
  <div class="shell">
    <!-- ── Nav rail ─────────────────────────────────────────────────────── -->
    <aside class="rail">
      <!-- Brand tile → home -->
      <button type="button" class="rail__brand" title="Tsundoku" aria-label="Tsundoku — home" @click="emit('navigate', homeKey)">
        <BrandMark :size="24" tone="inverse" />
      </button>

      <!-- Primary nav -->
      <nav class="rail__nav" aria-label="Primary">
        <button
          v-for="item in topItems"
          :key="item.key"
          type="button"
          class="rail__item"
          :class="{ 'rail__item--active': item.key === activeRoute }"
          :title="item.label"
          :aria-label="item.label"
          :aria-current="item.key === activeRoute ? 'page' : undefined"
          @click="emit('navigate', item.key)"
        >
          <Icon :name="`lucide:${item.icon}`" class="rail__icon" />
          <span
            v-if="item.badge && item.badge.count > 0"
            class="rail__badge"
            :class="`rail__badge--${item.badge.tone ?? 'danger'}`"
          >{{ item.badge.count }}</span>
        </button>
      </nav>

      <!-- Bottom-pinned controls -->
      <div class="rail__foot">
        <!-- Live download-activity indicators -->
        <div v-if="activeDownloads > 0" class="rail__activity rail__activity--active" :title="`${activeDownloads} active downloads`">
          <Icon name="lucide:download" class="rail__activity-icon" />
          <span class="rail__activity-count">{{ activeDownloads }}</span>
        </div>
        <div v-if="failedDownloads > 0" class="rail__activity rail__activity--failed" :title="`${failedDownloads} failed downloads`">
          <Icon name="lucide:triangle-alert" class="rail__activity-icon" />
          <span class="rail__activity-count">{{ failedDownloads }}</span>
        </div>

        <!-- Pinned nav (e.g. Settings) -->
        <button
          v-for="item in pinnedItems"
          :key="item.key"
          type="button"
          class="rail__item"
          :class="{ 'rail__item--active': item.key === activeRoute }"
          :title="item.label"
          :aria-label="item.label"
          :aria-current="item.key === activeRoute ? 'page' : undefined"
          @click="emit('navigate', item.key)"
        >
          <Icon :name="`lucide:${item.icon}`" class="rail__icon" />
          <span
            v-if="item.badge && item.badge.count > 0"
            class="rail__badge"
            :class="`rail__badge--${item.badge.tone ?? 'danger'}`"
          >{{ item.badge.count }}</span>
        </button>

        <!-- Theme toggle (glyph reflects the current theme) -->
        <button type="button" class="rail__ctl" :title="themeLabel" :aria-label="themeLabel" @click="emit('toggle-theme')">
          <Icon :name="themeIcon" class="rail__icon" />
        </button>

        <!-- Lock -->
        <button type="button" class="rail__ctl" title="Lock" aria-label="Lock" @click="emit('lock')">
          <Icon name="lucide:lock" class="rail__icon" />
        </button>
      </div>
    </aside>

    <!-- ── Main column ──────────────────────────────────────────────────── -->
    <div class="main">
      <header class="head">
        <div class="head__row">
          <div class="head__titles">
            <button v-if="breadcrumb" type="button" class="head__crumb" @click="emit('navigate', homeKey)">
              {{ breadcrumb }} <span aria-hidden="true">/</span>
            </button>
            <div class="head__title">{{ headerTitle }}</div>
          </div>

          <div class="head__actions">
            <!-- Sync indicator (announces politely while busy) -->
            <div v-if="syncing" class="head__sync" role="status" aria-live="polite" aria-busy="true">
              <span class="head__spinner" aria-hidden="true" />
              {{ syncLabel }}
            </div>

            <!-- "Need attention" pill → Library Health -->
            <button v-if="unhealthy > 0" type="button" class="head__attention" @click="emit('navigate', 'health')">
              <span class="head__attention-dot" aria-hidden="true" />
              {{ unhealthy }} need attention
            </button>

            <!-- Adopt a series -->
            <button type="button" class="head__adopt" @click="emit('open-adopt')">
              <Icon name="lucide:plus" class="head__adopt-icon" />
              Adopt series
            </button>
          </div>
        </div>

        <!-- Indeterminate mutation bar pinned to the header's bottom edge -->
        <div v-if="mutating" class="head__progress" aria-hidden="true">
          <div class="head__progress-bar" />
        </div>
      </header>

      <div class="content">
        <slot />
      </div>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  min-height: 100vh;
  height: 100%;
  background: var(--bg);
}

/* ── Nav rail ──────────────────────────────────────────────────────────── */
.rail {
  width: 72px;
  flex: none;
  background: var(--rail);
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 16px 0;
  gap: 9px;
  position: sticky;
  top: 0;
  height: 100vh;
  z-index: 30;
}

.rail__brand {
  width: 44px;
  height: 44px;
  border-radius: var(--radius-xl);
  border: none;
  background: linear-gradient(140deg, var(--accent), var(--accentDeep));
  box-shadow: var(--shadow-accent-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  margin-bottom: 8px;
}

.rail__nav {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 9px;
}

.rail__item {
  position: relative;
  width: 46px;
  height: 46px;
  border-radius: var(--radius-xl);
  border: 1px solid transparent;
  background: transparent;
  color: var(--muted);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: all 0.15s;
}

.rail__item:hover {
  color: var(--text);
  background: var(--surface2);
}

.rail__item--active {
  border-color: var(--border2);
  background: var(--accentSoft);
  color: var(--accentBright);
}

.rail__item:focus-visible,
.rail__ctl:focus-visible,
.rail__brand:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.rail__icon {
  width: 22px;
  height: 22px;
}

.rail__badge {
  position: absolute;
  top: -4px;
  right: -4px;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: var(--radius-pill);
  font-size: 10.5px;
  font-weight: var(--weight-extrabold);
  display: flex;
  align-items: center;
  justify-content: center;
  border: 2px solid var(--rail);
}

.rail__badge--danger {
  background: var(--danger);
  color: var(--on-danger);
}

.rail__badge--warn {
  background: var(--warn);
  color: var(--on-warn);
}

.rail__foot {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}

.rail__activity {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 1px;
}

.rail__activity--active {
  color: var(--accentBright);
}

.rail__activity--failed {
  color: var(--warn);
}

.rail__activity-icon {
  width: 20px;
  height: 20px;
  animation: pulseO 1.4s ease-in-out infinite;
}

.rail__activity-count {
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.rail__ctl {
  width: 42px;
  height: 42px;
  border-radius: var(--radius-lg);
  border: none;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s;
}

.rail__ctl:hover {
  background: var(--surface2);
  color: var(--text);
}

.rail__ctl .rail__icon {
  width: 20px;
  height: 20px;
}

/* ── Main column ───────────────────────────────────────────────────────── */
.main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.head {
  position: sticky;
  top: 0;
  z-index: 20;
  height: 64px;
  flex: none;
  padding: 0 26px;
  border-bottom: 1px solid var(--border);
  background: var(--bg2);
}

.head__row {
  height: 100%;
  display: flex;
  align-items: center;
  gap: 16px;
}

.head__titles {
  min-width: 0;
}

.head__crumb {
  display: flex;
  align-items: center;
  gap: 7px;
  margin-bottom: 1px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--faint);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
  cursor: pointer;
}

.head__crumb:hover {
  color: var(--accentBright);
}

.head__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-xl);
  line-height: var(--leading-tight);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 46vw;
}

.head__actions {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 10px;
}

.head__sync {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 7px 13px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  border: 1px solid var(--border);
  color: var(--accentBright);
  font-size: var(--text-sm);
  font-weight: var(--weight-semibold);
}

.head__spinner {
  width: 13px;
  height: 13px;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: var(--radius-pill);
  display: inline-block;
  animation: spin 0.8s linear infinite;
}

.head__attention {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 8px 13px;
  border-radius: var(--radius-pill);
  background: var(--danger-bg);
  border: 1px solid var(--danger-border);
  color: var(--danger-text);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
  transition: background 0.15s;
}

.head__attention:hover {
  background: var(--danger-bg-hover);
}

.head__attention-dot {
  width: 7px;
  height: 7px;
  border-radius: var(--radius-pill);
  background: var(--danger-bright);
  animation: pulseO 1.6s ease-in-out infinite;
}

.head__adopt {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 17px;
  border-radius: var(--radius-lg);
  background: linear-gradient(135deg, var(--accent), var(--accentDeep));
  border: none;
  color: var(--cover-text);
  font-size: var(--text-base);
  font-weight: var(--weight-bold);
  cursor: pointer;
  box-shadow: var(--shadow-accent);
  transition: filter 0.15s;
}

.head__adopt:hover {
  filter: brightness(1.08);
}

.head__adopt:focus-visible,
.head__attention:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.head__adopt-icon {
  width: 16px;
  height: 16px;
}

.head__progress {
  position: absolute;
  left: 0;
  bottom: -1px;
  height: 2px;
  width: 100%;
  overflow: hidden;
}

.head__progress-bar {
  height: 100%;
  width: 30%;
  background: var(--accentBright);
  animation: slide 1.1s ease-in-out infinite;
}

/* ── Screen content ────────────────────────────────────────────────────── */
.content {
  flex: 1;
  min-width: 0;
}
</style>
