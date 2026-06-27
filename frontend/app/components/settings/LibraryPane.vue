<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import DurationInput from '../ui/DurationInput.vue'
import LockedRow from '../ui/LockedRow.vue'
import SaveFooter from '../ui/SaveFooter.vue'
import type { LibrarySettings, SaveState, SystemInfo } from '../screens/settings.types'

/**
 * LibraryPane — the "Schedules & Behavior" pane: the runtime-editable library
 * knobs (three duration rows + three integer rows + an advanced disclosure) and
 * the read-only System card of deploy-time facts.
 *
 * Keeps a LOCAL editable copy seeded from `library`; Save emits that copy, and
 * when the parent reflects the persisted value back the copy re-seeds (§16
 * round-trip). The Save button disables until the copy is dirty.
 *
 *   - `library`: the runtime-editable knobs (the source of truth).
 *   - `system`: read-only deploy-time facts for the System card.
 *   - `save`: the §16 save lifecycle (loading / success / error).
 *
 * Emits `save` with the full edited copy.
 */
const props = withDefaults(defineProps<{
  /** The runtime-editable library knobs. */
  library: LibrarySettings
  /** Read-only deploy-time facts (env-sourced). */
  system: SystemInfo
  /** §16 state of the Save button. */
  save?: SaveState
}>(), {
  save: () => ({ status: 'idle' }),
})

const emit = defineEmits<{
  /** Persist the edited knobs — carries the full edited copy. */
  save: [settings: LibrarySettings]
}>()

// Deep-clone so the local copy is fully detached from the prop object.
const cloneLibrary = (l: LibrarySettings): LibrarySettings => ({
  refreshInterval: { ...l.refreshInterval },
  downloadInterval: { ...l.downloadInterval },
  retryBackoff: { ...l.retryBackoff },
  maxRetries: l.maxRetries,
  staleGraceDays: l.staleGraceDays,
  refreshConcurrency: l.refreshConcurrency,
})

const lib = reactive(cloneLibrary(props.library))

// Re-seed on every source-of-truth change (post-save rehydrate, §16): dirty
// resets to false once the persisted values flow back.
watch(() => props.library, v => Object.assign(lib, cloneLibrary(v)), { deep: true })

const dirty = computed(() => JSON.stringify(lib) !== JSON.stringify(props.library))

// SaveFooter speaks the ui SaveState (`error`); the screen prop carries `message`.
const footerState = computed(() => ({ status: props.save.status, error: props.save.message }))

const advancedOpen = ref(false)

// Clamp a raw integer-field input to a non-negative integer (NaN / negatives → 0).
const clampInt = (raw: string): number => Math.max(0, Number.parseInt(raw, 10) || 0)

function onSave() {
  if (!dirty.value || props.save.status === 'saving') return
  emit('save', cloneLibrary(lib))
}
</script>

<template>
  <section class="card">
    <h2 class="card__title">Schedules &amp; Behavior</h2>
    <p class="card__sub">Runtime-editable timing. The job schedulers re-read these on the next tick.</p>

    <div class="srow">
      <div class="srow__label">
        <div class="srow__name">Refresh interval</div>
        <div class="srow__hint">How often to poll titles for new chapters</div>
      </div>
      <DurationInput v-model="lib.refreshInterval" />
    </div>

    <div class="srow">
      <div class="srow__label">
        <div class="srow__name">Download interval</div>
        <div class="srow__hint">Queue-drain &amp; upgrade-swap cadence</div>
      </div>
      <DurationInput v-model="lib.downloadInterval" />
    </div>

    <div class="srow">
      <div class="srow__label">
        <div class="srow__name">Chapter retry backoff</div>
        <div class="srow__hint">Wait before retrying a failed chapter</div>
      </div>
      <DurationInput v-model="lib.retryBackoff" />
    </div>

    <div class="srow">
      <div class="srow__label">
        <div class="srow__name">Chapter max retries</div>
        <div class="srow__hint">Attempts before a chapter is permanently failed</div>
      </div>
      <input class="num-input" type="number" min="0" :value="lib.maxRetries" @input="lib.maxRetries = clampInt(($event.target as HTMLInputElement).value)">
    </div>

    <div class="srow">
      <div class="srow__label">
        <div class="srow__name">Stale-grace days</div>
        <div class="srow__hint">Health threshold before a source counts as stale</div>
      </div>
      <input class="num-input" type="number" min="0" :value="lib.staleGraceDays" @input="lib.staleGraceDays = clampInt(($event.target as HTMLInputElement).value)">
    </div>

    <div class="advanced">
      <button type="button" class="advanced__toggle" @click="advancedOpen = !advancedOpen">
        <svg class="advanced__chev" :class="{ 'advanced__chev--open': advancedOpen }" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 18l6-6-6-6" /></svg>
        Advanced
      </button>
      <div v-if="advancedOpen" class="srow srow--advanced">
        <div class="srow__label">
          <div class="srow__name">Refresh concurrency</div>
          <div class="srow__hint">Parallel source fetches — be gentle on sources</div>
        </div>
        <input class="num-input" type="number" min="0" :value="lib.refreshConcurrency" @input="lib.refreshConcurrency = clampInt(($event.target as HTMLInputElement).value)">
      </div>
    </div>

    <SaveFooter :state="footerState" :dirty="dirty" label="Save changes" @save="onSave" />
  </section>

  <section class="card">
    <h2 class="card__title">System</h2>
    <p class="card__sub">Set at deploy time via environment variables — read-only here.</p>
    <LockedRow label="Storage folder" :value="system.storageFolder" />
    <LockedRow label="Server port" :value="system.serverPort" />
    <LockedRow label="Database" :value="system.database" />
  </section>
</template>

<style scoped>
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-2xl);
  padding: 20px;
  margin-bottom: 16px;
}

.card:last-child {
  margin-bottom: 0;
}

.card__title {
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
  font-size: var(--text-lg);
  color: var(--text);
  margin: 0;
}

.card__sub {
  font-size: 12.5px;
  color: var(--faint);
  margin: 2px 0 8px;
}

/* ---- Setting row (label + control) ---------------------------------------- */
.srow {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 13px 0;
  border-top: 1px solid var(--border);
}

.srow--advanced {
  border-top: none;
  padding: 13px 0 2px;
}

.srow__name {
  font-size: 13.5px;
  font-weight: var(--weight-bold);
  color: var(--text);
}

.srow__hint {
  font-size: 11.5px;
  color: var(--faint);
}

/* ---- Bare integer input (inline, fixed-width) ----------------------------- */
.num-input {
  width: 80px;
  flex: none;
  padding: 9px 11px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border2);
  background: var(--bg2);
  color: var(--text);
  font-family: var(--font-sans);
  font-size: var(--text-base);
  outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}

.num-input:focus {
  border-color: var(--accent);
  box-shadow: var(--ring-focus);
}

/* ---- Advanced disclosure -------------------------------------------------- */
.advanced {
  border-top: 1px solid var(--border);
  padding-top: 11px;
  margin-top: 2px;
}

.advanced__toggle {
  display: flex;
  align-items: center;
  gap: 7px;
  background: none;
  border: none;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-weight: var(--weight-bold);
  cursor: pointer;
  padding: 0;
}

.advanced__chev {
  transition: transform 0.15s;
}

.advanced__chev--open {
  transform: rotate(90deg);
}
</style>
