<script setup lang="ts">
import { computed } from 'vue'
import BrandMark from '../ui/BrandMark.vue'
import AppButton from '../ui/AppButton.vue'
import ProgressBar from '../ui/ProgressBar.vue'
import Spinner from '../ui/Spinner.vue'
import NavRailItem from './NavRailItem.vue'
import RailActivityIndicator from './RailActivityIndicator.vue'
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
        <NavRailItem
          v-for="item in topItems"
          :key="item.key"
          :icon="item.icon"
          :label="item.label"
          :active="item.key === activeRoute"
          :badge="item.badge"
          @select="emit('navigate', item.key)"
        />
      </nav>

      <!-- Bottom-pinned controls -->
      <div class="rail__foot">
        <!-- Live download-activity indicators -->
        <RailActivityIndicator :active="activeDownloads" :failed="failedDownloads" />

        <!-- Pinned nav (e.g. Settings) -->
        <NavRailItem
          v-for="item in pinnedItems"
          :key="item.key"
          :icon="item.icon"
          :label="item.label"
          :active="item.key === activeRoute"
          :badge="item.badge"
          @select="emit('navigate', item.key)"
        />

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
              <Spinner :size="13" aria-hidden="true" />
              {{ syncLabel }}
            </div>

            <!-- "Need attention" pill → Library Health -->
            <button v-if="unhealthy > 0" type="button" class="head__attention" @click="emit('navigate', 'health')">
              <span class="head__attention-dot" aria-hidden="true" />
              {{ unhealthy }} need attention
            </button>

            <!-- Adopt a series -->
            <AppButton variant="primary" @click="emit('open-adopt')">
              <template #icon><Icon name="lucide:plus" class="head__adopt-icon" /></template>
              Adopt series
            </AppButton>
          </div>
        </div>

        <!-- Indeterminate mutation bar pinned to the header's bottom edge -->
        <div v-if="mutating" class="head__progress" aria-hidden="true">
          <ProgressBar track="transparent" tone="var(--accentBright)" />
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

.rail__ctl:focus-visible,
.rail__brand:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.rail__foot {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
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

.head__attention:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

/* The Adopt CTA is an AppButton; this only sizes the lucide glyph in its
   #icon slot (slotted content compiles in this parent scope). */
.head__adopt-icon {
  width: 16px;
  height: 16px;
}

/* Layout-only wrapper that pins the indeterminate mutation bar (a ProgressBar)
   to the header's bottom edge and clips it to a 2px hairline. */
.head__progress {
  position: absolute;
  left: 0;
  bottom: -1px;
  height: 2px;
  width: 100%;
  overflow: hidden;
}

/* ── Screen content ────────────────────────────────────────────────────── */
.content {
  flex: 1;
  min-width: 0;
}
</style>
