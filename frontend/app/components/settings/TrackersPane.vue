<script setup lang="ts">
import FormError from '../ui/FormError.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TrackerRow from './TrackerRow.vue'
import type { TrackerActionState, TrackerStatus } from '../screens/settings.types'

/**
 * TrackersPane — the Settings → Trackers pane (Phase 3d): connect/disconnect the
 * four native trackers (AniList, MAL, Kitsu, MangaUpdates). Presentation-only:
 * the page owns the data (useTrackers) and every mutation is emitted — this pane
 * never fetches or navigates itself (the OAuth full-tab redirect is the page's
 * job, since only the page can `window.location.href` after a successful
 * `authUrl()` resolve).
 *
 *   - `trackers`: every tracker's connect status.
 *   - `trackerAction`: §16 state of the one in-flight connect/login/logout
 *     action (busy tracker id + error) — surfaced on the matching row only.
 *   - `misconfiguredIds`: OAuth trackers whose last connect attempt found no
 *     client-id/public-URL configured (drives the "Not configured" row shape).
 *   - `redirectUrl`: the callback URL to register, shown on a misconfigured row.
 *   - `pending`/`error`: the initial list-load state.
 */
withDefaults(defineProps<{
  /** Every registered tracker's connect status. */
  trackers: TrackerStatus[]
  /** §16 state of the one in-flight connect/login/logout action. */
  trackerAction?: TrackerActionState
  /** Tracker ids known to be missing OAuth app config. */
  misconfiguredIds?: number[]
  /** The callback URL the owner must register with each OAuth tracker's app. */
  redirectUrl?: string
  /** Whether the tracker list is loading. */
  pending?: boolean
  /** A list-load failure, surfaced inline. */
  error?: string | null
}>(), {
  trackerAction: () => ({ busyId: null }),
  misconfiguredIds: () => [],
  redirectUrl: '',
  pending: false,
  error: null,
})

const emit = defineEmits<{
  /** The OAuth "Connect" button was pressed for a tracker id. */
  connect: [trackerId: number]
  /** A credential sign-in form was submitted — carries the tracker id + pair. */
  'login-credentials': [payload: { trackerId: number, username: string, password: string }]
  /** The "Disconnect" button was pressed for a tracker id. */
  logout: [trackerId: number]
}>()

// A few skeleton rows while the tracker list loads.
const skeletons = [0, 1, 2, 3]
</script>

<template>
  <SurfaceCard
    title="Trackers"
    sub="Connect AniList, MAL, Kitsu, or MangaUpdates to bind your series and track reading progress."
  >
    <!-- Loading skeletons -->
    <div v-if="pending" class="tracker-list">
      <div v-for="n in skeletons" :key="n" class="skeleton-row" />
    </div>

    <!-- Load error -->
    <div v-else-if="error" class="load-error">
      <FormError :message="error" />
    </div>

    <!-- Empty (defensive — the backend always lists all four registered trackers) -->
    <p v-else-if="trackers.length === 0" class="tracker-empty">
      No trackers registered.
    </p>

    <!-- The tracker rows -->
    <template v-else>
      <!-- §16 pane-level mutation failure (mirrors ExtensionsPane's extensionAction.error) —
           the row that caused it is no longer identifiable once its busy flag clears, so
           this is a single message above the list rather than a per-row banner. -->
      <FormError v-if="trackerAction.error" class="tracker-error" :message="trackerAction.error" />

      <div class="tracker-list">
        <TrackerRow
          v-for="t in trackers"
          :key="t.id"
          :tracker="t"
          :busy="trackerAction.busyId === t.id"
          :misconfigured="misconfiguredIds.includes(t.id)"
          :redirect-url="redirectUrl"
          @connect="emit('connect', t.id)"
          @login="(payload) => emit('login-credentials', { trackerId: t.id, ...payload })"
          @logout="emit('logout', t.id)"
        />
      </div>
    </template>
  </SurfaceCard>
</template>

<style scoped>
.tracker-error {
  margin-bottom: 12px;
}

.tracker-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.load-error {
  margin-top: 4px;
}

.tracker-empty {
  padding: 14px 2px;
  font-size: var(--text-sm);
  color: var(--muted);
}

/* ---- Loading skeletons ---------------------------------------------------- */
.skeleton-row {
  height: 76px;
  border-radius: var(--radius-lg);
  background: var(--surface2);
  position: relative;
  overflow: hidden;
}

.skeleton-row::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: tracker-shimmer 1.4s ease-in-out infinite;
}

@keyframes tracker-shimmer {
  to { transform: translateX(100%); }
}
</style>
