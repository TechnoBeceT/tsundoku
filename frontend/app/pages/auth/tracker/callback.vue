<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useTrackers } from '~/composables/useTrackers'
import { takePendingTrackerId } from '~/utils/trackerCallback'

definePageMeta({ layout: 'bare' })

/**
 * Tracker OAuth callback — /auth/tracker/callback.
 *
 * Both supported OAuth trackers land here after the owner authorizes on the
 * provider's own site: MAL redirects with `?code=&state=` in the query string;
 * AniList's implicit grant carries `#access_token=&state=` in the URL FRAGMENT,
 * which only ever reaches the BROWSER (a server never sees it). So this page
 * reads the FULL `window.location.href` client-side and hands the whole string
 * to `loginOAuth`, which POSTs it to the backend to extract whichever shape
 * applies (spec/trackers-oauth-phase3 §4) — the token itself is never read or
 * displayed here, only forwarded.
 *
 * `state` is an OPAQUE, server-generated token (stashed server-side against the
 * tracker's pending PKCE verifier by `GET /api/trackers/{id}/auth-url`) — it does
 * NOT carry the tracker id back to this ONE SHARED callback route. The
 * "Connect" click (Settings → Trackers) stashes the tracker id in
 * sessionStorage immediately before the full-tab navigate away; this page reads
 * it back (same tab, survives the round trip — see trackerCallback.ts) via
 * `takePendingTrackerId()`. A missing value (a stale bookmark, a second tab, a
 * replayed visit — the read is one-shot) surfaces as a friendly error instead
 * of guessing which tracker it belongs to.
 *
 * On success or failure this redirects back to Settings → Trackers, carrying
 * the outcome via `?trackersFlash=` (see pages/settings.vue's own doc comment)
 * rather than trying to render the full Settings screen from this bare page.
 */
const { loginOAuth } = useTrackers()

const status = ref<'working' | 'error'>('working')
const message = ref('')

onMounted(async () => {
  const params = new URLSearchParams(window.location.search)
  const providerError = params.get('error')
  if (providerError) {
    await finish(false, `The tracker declined the connection (${providerError}).`)
    return
  }

  const trackerId = takePendingTrackerId()
  if (trackerId == null) {
    await finish(false, 'Could not tell which tracker this belongs to — try connecting again.')
    return
  }

  const ok = await loginOAuth(trackerId, window.location.href)
  await finish(ok, ok ? '' : 'The tracker connection failed — try again.')
})

/** Shows the outcome briefly, then hands off to Settings → Trackers via a flash query param. */
async function finish(ok: boolean, errorMessage: string): Promise<void> {
  if (!ok) {
    status.value = 'error'
    message.value = errorMessage
  }
  const query = ok
    ? { trackersFlash: 'connected' }
    : { trackersFlash: 'error', trackersFlashMessage: errorMessage }
  await new Promise((resolve) => setTimeout(resolve, ok ? 400 : 1800))
  await navigateTo({ path: '/settings', query })
}
</script>

<template>
  <div class="callback">
    <div class="callback__card">
      <p v-if="status === 'working'" class="callback__status">Connecting your tracker account…</p>
      <ErrorBanner v-else :message="message" :dismissible="false" />
      <p v-if="status === 'error'" class="callback__hint">Redirecting back to Settings…</p>
    </div>
  </div>
</template>

<style scoped>
.callback {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100dvh;
  padding: 24px;
  background: var(--bg);
}

.callback__card {
  max-width: 420px;
  width: 100%;
  text-align: center;
}

.callback__status {
  margin: 0;
  color: var(--muted);
  font-size: var(--text-base);
}

.callback__hint {
  margin: 10px 0 0;
  color: var(--faint);
  font-size: var(--text-sm);
}
</style>
