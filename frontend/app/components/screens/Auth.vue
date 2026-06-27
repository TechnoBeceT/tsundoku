<script setup lang="ts">
import { computed, ref } from 'vue'
import BrandLockup from '../ui/BrandLockup.vue'

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
 * button shows an in-flight spinner and the error renders as an aria-live
 * message. `switch-mode` toggles between claim and login (parent owns `mode`).
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

// The server error takes precedence; otherwise show any client-side guard message.
const displayedError = computed(() => props.error || localError.value)

// Clear the client-side guard message whenever the owner changes the inputs.
const clearLocalError = (): void => {
  localError.value = ''
}

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
        <div class="field">
          <label class="field__label" for="auth-username">Username</label>
          <input
            id="auth-username"
            v-model="username"
            class="field__input"
            type="text"
            autocomplete="username"
            placeholder="owner"
            :disabled="loading"
            @input="clearLocalError"
          >
        </div>

        <div class="field">
          <label class="field__label" for="auth-password">Password</label>
          <input
            id="auth-password"
            v-model="password"
            class="field__input"
            type="password"
            :autocomplete="isClaim ? 'new-password' : 'current-password'"
            placeholder="••••••••"
            :disabled="loading"
            @input="clearLocalError"
          >
        </div>

        <!-- Error region: always present so the aria-live announcement fires. -->
        <p class="auth__error" role="alert" aria-live="polite">
          <template v-if="displayedError">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true">
              <circle cx="12" cy="12" r="9" />
              <path d="M12 8v4" />
              <path d="M12 16h.01" />
            </svg>
            {{ displayedError }}
          </template>
        </p>

        <button class="auth__submit" type="submit" :disabled="loading">
          <span v-if="loading" class="auth__spinner" aria-hidden="true" />
          {{ cta }}
        </button>
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
  min-height: 100vh;
  padding: 24px;
  overflow: hidden;
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

.field {
  margin-bottom: 14px;
}

.field__label {
  display: block;
  margin-bottom: 7px;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  color: var(--faint);
}

.field__input {
  width: 100%;
  padding: 12px 14px;
  border-radius: var(--radius-lg);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-md);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.field__input::placeholder {
  color: var(--faint);
}

.field__input:focus {
  border-color: var(--accent);
  box-shadow: 0 0 0 3px var(--accentSoft);
}

.field__input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

/* ---- Error ---------------------------------------------------------------- */
.auth__error {
  display: flex;
  align-items: center;
  gap: 7px;
  min-height: 19px;
  margin: 6px 0 0;
  font-size: var(--text-sm);
  color: var(--danger-text);
}

/* ---- Submit --------------------------------------------------------------- */
.auth__submit {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 9px;
  width: 100%;
  margin-top: 18px;
  padding: 13px;
  border: none;
  border-radius: var(--radius-lg);
  background: linear-gradient(135deg, var(--accent), var(--accentDeep));
  color: var(--cover-text);
  font-family: var(--font-sans);
  font-size: 14.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  box-shadow: 0 8px 20px -8px var(--accent);
  transition: filter 0.15s;
}

.auth__submit:hover:not(:disabled) {
  filter: brightness(1.08);
}

.auth__submit:focus-visible {
  outline: none;
  box-shadow: 0 0 0 3px var(--accentSoft), 0 8px 20px -8px var(--accent);
}

.auth__submit:disabled {
  cursor: progress;
  opacity: 0.85;
}

.auth__spinner {
  width: 15px;
  height: 15px;
  border: 2px solid var(--cover-text);
  border-right-color: transparent;
  border-radius: var(--radius-pill);
  /* `spin` is a global keyframe (base.css). */
  animation: spin 0.8s linear infinite;
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
</style>
