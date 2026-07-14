<script setup lang="ts">
import { ref } from 'vue'
import AppButton from '../ui/AppButton.vue'
import LockedRow from '../ui/LockedRow.vue'
import Tag from '../ui/Tag.vue'
import TextField from '../ui/TextField.vue'
import TrackerIcon from '../ui/TrackerIcon.vue'
import type { TrackerStatus } from '../screens/settings.types'

/**
 * TrackerRow — one tracker's connect card on the Settings → Trackers pane.
 * The head row always shows the tracker's brand logo (`TrackerIcon`, QCAT-237
 * reuse — the same atom `TrackerBindingRow`/`TrackersSection` use) beside its
 * name. Three mutually-exclusive shapes, picked by `tracker`'s own state:
 *   - Connected (`isLoggedIn`): username + a status Tag (Connected / Token
 *     expired) + a "Disconnect" button.
 *   - Disconnected + `needsOAuth` (AniList/MAL): a "Connect" button that the
 *     parent turns into a full-tab redirect — UNLESS the last attempt revealed
 *     no client-id/public-URL configured (`misconfigured`), in which case this
 *     shows "Not configured" plus the redirect URL the owner must register
 *     with that tracker's OAuth app instead of a dead-end button.
 *   - Disconnected + credential-based (Kitsu/MangaUpdates): an inline
 *     username/password form emitting `login` on submit.
 *
 * Presentation-only: the parent (TrackersPane) owns all mutation state and
 * this row only emits intent — a mutation failure is surfaced by the PARENT as
 * one pane-level message (mirrors ExtensionsPane's `extensionAction.error`),
 * not a per-row banner, so this row carries no `error` prop. The credential
 * form fields are LOCAL UI state (mirrors CategoriesPane's add-row input) —
 * nothing worth lifting since the parent only needs the final submitted pair.
 *
 *   - `tracker`: the tracker to render.
 *   - `busy`: this row's action (connect/login/logout) is in flight.
 *   - `misconfigured`: an OAuth tracker whose last `connect` attempt found no
 *     client-id/public-URL configured.
 *   - `redirectUrl`: the callback URL to register (shown when misconfigured).
 *
 * Emits `connect` (OAuth Connect pressed), `login` (credential form submitted,
 * carries `{ username, password }`), `logout` (Disconnect pressed).
 */
withDefaults(defineProps<{
  /** The tracker to render. */
  tracker: TrackerStatus
  /** This row's action (connect/login/logout) is in flight. */
  busy?: boolean
  /** An OAuth tracker with no client-id/public-URL configured yet. */
  misconfigured?: boolean
  /** The callback URL to register with the tracker's OAuth app. */
  redirectUrl?: string
}>(), {
  busy: false,
  misconfigured: false,
  redirectUrl: '',
})

const emit = defineEmits<{
  /** The OAuth "Connect" button was pressed. */
  connect: []
  /** The credential sign-in form was submitted — carries the entered pair. */
  login: [payload: { username: string, password: string }]
  /** The "Disconnect" button was pressed. */
  logout: []
}>()

const username = ref('')
const password = ref('')

function submitLogin(): void {
  if (!username.value.trim() || !password.value) return
  emit('login', { username: username.value.trim(), password: password.value })
}
</script>

<template>
  <div class="tracker-row" :class="{ 'tracker-row--busy': busy }">
    <div class="tracker-row__head">
      <TrackerIcon :tracker-id="tracker.id" />
      <span class="tracker-row__name">{{ tracker.name }}</span>
      <Tag v-if="tracker.isLoggedIn && tracker.isTokenExpired" tone="warn">Token expired</Tag>
      <Tag v-else-if="tracker.isLoggedIn" tone="success">Connected</Tag>
      <Tag v-else tone="neutral">Not connected</Tag>
    </div>

    <p v-if="tracker.isLoggedIn" class="tracker-row__username">{{ tracker.username }}</p>

    <!-- Connected: a single Disconnect action, regardless of OAuth/credential shape. -->
    <div v-if="tracker.isLoggedIn" class="tracker-row__actions">
      <AppButton variant="danger-ghost" size="sm" :loading="busy" @click="emit('logout')">
        Disconnect
      </AppButton>
    </div>

    <!-- Disconnected, OAuth (AniList/MAL). -->
    <template v-else-if="tracker.needsOAuth">
      <div v-if="misconfigured" class="tracker-row__notconfigured">
        <Tag tone="neutral">Not configured</Tag>
        <p class="tracker-row__hint">
          Register this redirect URL with {{ tracker.name }}'s OAuth app, then set its client-id in Tsundoku's config.
        </p>
        <LockedRow label="Redirect URL" :value="redirectUrl" plain />
      </div>
      <div v-else class="tracker-row__actions">
        <AppButton variant="solid" size="sm" :loading="busy" @click="emit('connect')">
          Connect
        </AppButton>
      </div>
    </template>

    <!-- Disconnected, credential-based (Kitsu/MangaUpdates). -->
    <form v-else class="tracker-row__form" @submit.prevent="submitLogin">
      <TextField
        v-model="username"
        placeholder="Username"
        autocomplete="username"
        :disabled="busy"
      />
      <TextField
        v-model="password"
        type="password"
        placeholder="Password"
        autocomplete="current-password"
        :disabled="busy"
      />
      <AppButton type="submit" variant="solid" size="sm" :loading="busy" :disabled="!username.trim() || !password">
        Sign in
      </AppButton>
    </form>
  </div>
</template>

<style scoped>
.tracker-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: 14px;
}

.tracker-row--busy {
  opacity: 0.75;
}

.tracker-row__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.tracker-row__name {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
}

.tracker-row__username {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--muted);
  overflow-wrap: anywhere;
}

.tracker-row__actions {
  display: flex;
  gap: 8px;
}

.tracker-row__notconfigured {
  display: flex;
  flex-direction: column;
  gap: 6px;
  align-items: flex-start;
}

.tracker-row__hint {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--muted);
}

.tracker-row__form {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  flex-wrap: wrap;
}

.tracker-row__form :deep(.field) {
  min-width: 140px;
  flex: 1;
}

@media (max-width: 900px) {
  /* Three inline controls (two fields + a button) have no room on a phone —
   * stack them full-width instead of crushing the inputs (QCAT-230). */
  .tracker-row__form {
    flex-direction: column;
    align-items: stretch;
  }

  .tracker-row__form :deep(.field) {
    min-width: 0;
  }
}
</style>
