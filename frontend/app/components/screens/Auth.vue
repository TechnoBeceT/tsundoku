<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import BrandLockup from '../ui/BrandLockup.vue'
import FormError from '../ui/FormError.vue'
import TextField from '../ui/TextField.vue'

/**
 * Auth — the bare, pre-boot authentication card shown before the app shell
 * loads: a single screen covering BOTH first-run owner claim and returning-owner
 * login, selected by the `mode` prop. It floats a brand-lockup + title/sub +
 * username/password form on a two-glow radial backdrop (token-driven).
 *
 * Presentation only: the fields are internal refs and the screen never fetches.
 * It runs the same lightweight client-side guards as the prototype (non-empty
 * username; non-empty password; password ≥ 8 in claim mode) and, when they pass,
 * emits `submit` with the credentials for the parent to POST. Server-side
 * outcomes arrive back via the `loading` + `error` props (§16): the submit
 * button shows an in-flight spinner and the error renders as an inline message.
 * `switch-mode` toggles between claim and login (parent owns `mode`).
 *
 * Composes the shared atoms: `TextField` (credential inputs), `AppButton`
 * (primary submit with its own loading spinner), `FormError` (the inline guard /
 * server message), `BrandLockup` (header). Only the screen-unique backdrop + card
 * layout lives here.
 *
 * Endpoints (parent wiring, here for reference): claim → POST /api/owner/claim
 * (409 ⇒ owner exists ⇒ switch to login); login → POST /api/owner/login
 * (401 ⇒ generic "Invalid credentials").
 */
const props = withDefaults(defineProps<{
  /** Which flow to render: first-run owner creation, or returning login. */
  mode: 'claim' | 'login'
  /** True while the parent's claim/login request is in flight. */
  loading?: boolean
  /** A server (or higher-level) error message to surface, or "" for none. */
  error?: string
}>(), {
  loading: false,
  error: '',
})

const emit = defineEmits<{
  /** Client-side guards passed — carries the entered credentials to POST. */
  submit: [credentials: { username: string; password: string }]
  /** The owner clicked the mode-switch link (claim ⇆ login). */
  'switch-mode': []
}>()

// The form fields live here — this is a self-contained presentation screen.
const username = ref('')
const password = ref('')

// A client-side validation message (distinct from the server `error` prop). It
// is cleared as soon as the owner edits a field or switches mode.
const localError = ref('')

// Copy that differs between the two flows (mirrors the prototype's auth strings).
const isClaim = computed(() => props.mode === 'claim')
const title = computed(() => (isClaim.value ? 'Claim this install' : 'Welcome back'))
const subtitle = computed(() =>
  isClaim.value
    ? 'Create the owner account. This is a one-time setup.'
    : 'Sign in to manage your library.',
)
const cta = computed(() => (isClaim.value ? 'Create owner account' : 'Sign in'))
const switchText = computed(() =>
  isClaim.value ? 'Already set up? Sign in instead' : 'First run? Claim this install',
)
// Claim mints a brand-new credential (`new-password` keeps managers from
// offering the old one); login recalls the stored one (`current-password`).
const passwordAutocomplete = computed(() => (isClaim.value ? 'new-password' : 'current-password'))

// The server error takes precedence; otherwise show any client-side guard message.
const displayedError = computed(() => props.error || localError.value)

// Clear the client-side guard message whenever the owner changes the inputs.
const clearLocalError = (): void => {
  localError.value = ''
}
watch([username, password], clearLocalError)

// Run the same guards as the prototype, then emit. Claim enforces an 8-char
// minimum password "visually" by surfacing the rule as the error message.
const onSubmit = (): void => {
  if (!username.value.trim()) {
    localError.value = 'Enter a username'
    return
  }
  if (isClaim.value && password.value.length < 8) {
    localError.value = 'Password must be at least 8 characters'
    return
  }
  if (!password.value) {
    localError.value = 'Enter your password'
    return
  }
  localError.value = ''
  emit('submit', { username: username.value, password: password.value })
}

const onSwitch = (): void => {
  localError.value = ''
  emit('switch-mode')
}
</script>

<template>
  <div class="auth">
    <!-- Two-glow radial backdrop (token-driven, theme-independent). -->
    <div class="auth__backdrop" aria-hidden="true" />

    <div class="auth__card">
      <BrandLockup class="auth__brand" />

      <h1 class="auth__title">{{ title }}</h1>
      <p class="auth__sub">{{ subtitle }}</p>

      <form class="auth__form" novalidate @submit.prevent="onSubmit">
        <TextField
          v-model="username"
          class="auth__field"
          label="Username"
          placeholder="owner"
          autocomplete="username"
          name="username"
          :disabled="loading"
        />

        <TextField
          v-model="password"
          class="auth__field"
          label="Password"
          type="password"
          placeholder="••••••••"
          :autocomplete="passwordAutocomplete"
          name="password"
          :disabled="loading"
        />

        <!-- Reserved region so the surfacing error never shifts the button. -->
        <div class="auth__error">
          <FormError v-if="displayedError" :message="displayedError" />
        </div>

        <AppButton class="auth__submit" type="submit" variant="primary" size="lg" :loading="loading">
          {{ cta }}
        </AppButton>
      </form>

      <div class="auth__switch">
        <button type="button" class="auth__switch-link" @click="onSwitch">{{ switchText }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Pull in the auth-only backdrop tokens so the screen is self-contained in
 * Storybook; the parent also wires this into the global index.css. */
@import '../../assets/css/tokens/auth.css';

.auth {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  /* dvh (not vh): on mobile, vh is pinned to the layout viewport and ignores
   * on-screen chrome (address bar / keyboard), so a short visual viewport
   * (mobile landscape, keyboard up) can leave the card partially offscreen.
   * dvh tracks the ACTUAL visible viewport (the codebase's existing
   * convention — see Discover/SeriesDetail/Downloads/LibraryList). */
  min-height: 100dvh;
  padding: 24px;
  /* Vertical overflow must SCROLL, never clip: a min-height box already
   * grows to fit taller content, but `overflow: hidden` here would still
   * clip the card on a viewport shorter than its content (small phone +
   * on-screen keyboard) — the login must always stay reachable. Horizontal
   * stays clipped since the backdrop is an inset:0 box that never needs to
   * overflow sideways. */
  overflow-x: hidden;
  overflow-y: auto;
  background: var(--bg);
}

.auth__backdrop {
  position: absolute;
  inset: 0;
  background: var(--auth-backdrop);
}

/* ---- Card ----------------------------------------------------------------- */
.auth__card {
  position: relative;
  width: 100%;
  max-width: 404px;
  padding: 34px;
  border-radius: var(--radius-3xl);
  background: var(--surface);
  border: 1px solid var(--border);
  box-shadow: var(--shadow);
}

.auth__brand {
  margin-bottom: 26px;
}

.auth__title {
  margin: 0 0 5px;
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-2xl);
  color: var(--text);
}

.auth__sub {
  margin: 0 0 24px;
  font-size: var(--text-base);
  color: var(--muted);
  line-height: var(--leading-normal);
}

/* ---- Form ----------------------------------------------------------------- */
.auth__form {
  display: flex;
  flex-direction: column;
}

.auth__field {
  margin-bottom: 14px;
}

/* ---- Error ---------------------------------------------------------------- */
.auth__error {
  min-height: 19px;
  margin-top: 6px;
}

/* ---- Submit --------------------------------------------------------------- */
.auth__submit {
  width: 100%;
  margin-top: 18px;
}

/* ---- Mode switch ---------------------------------------------------------- */
.auth__switch {
  margin-top: 17px;
  text-align: center;
}

.auth__switch-link {
  padding: 0;
  border: none;
  background: none;
  color: var(--accentBright);
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-weight: var(--weight-semibold);
  cursor: pointer;
}

.auth__switch-link:hover {
  text-decoration: underline;
}

/* ---- Responsive (QCAT-230) -------------------------------------------------
 * The card is already fluid (width:100%, max-width:404px) and single-column
 * (brand → title → form → switch link stacked top-to-bottom — there is no
 * side-by-side split to break apart). At phone width the only real fix is
 * tightening the outer/card padding so the card isn't squeezed by its own
 * chrome on a narrow screen (390px - 2*24px outer - 2*34px card padding left
 * very little room for the inputs). */
@media (max-width: 900px) {
  .auth {
    padding: 16px;
  }

  .auth__card {
    padding: 24px 20px;
  }
}
</style>
