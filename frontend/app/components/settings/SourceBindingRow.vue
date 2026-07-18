<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import SelectField from '../ui/SelectField.vue'
import Spinner from '../ui/Spinner.vue'
import type { SelectOption } from '../ui/forms.types'
import type { FlareMode, NetworkEndpoint, NetworkSource, SourceBinding } from '../screens/settings.types'

/**
 * SourceBindingRow — one source's network-routing assignment: the source name +
 * language, a SOCKS `<select>` (Global default / each SOCKS endpoint), a
 * FlareSolverr `<select>` (None / Global default / each FlareSolverr endpoint),
 * and a "Use global default" button that clears the binding entirely.
 *
 * A source with NO binding is the global default: both selects sit on their
 * default option and Clear is disabled (there is nothing to clear). Changing
 * either select emits the FULL merged binding (`set`) so the other dimension is
 * never dropped (§16). `busy` disables the row's controls + spins while its own
 * mutation runs.
 *
 *   - `source`: the engine source this row addresses.
 *   - `binding`: the source's current binding, or null when unbound (= global).
 *   - `socksEndpoints` / `flareEndpoints`: the selectable endpoints per dimension.
 *   - `busy`: this row's mutation is in flight.
 *
 * Emits `set` (the full merged binding) and `clear` (revert to global default).
 */
const props = withDefaults(defineProps<{
  /** The engine source this row addresses. */
  source: NetworkSource
  /** The source's current binding, or null when unbound (global default). */
  binding?: SourceBinding | null
  /** The selectable SOCKS endpoints. */
  socksEndpoints: NetworkEndpoint[]
  /** The selectable FlareSolverr endpoints. */
  flareEndpoints: NetworkEndpoint[]
  /** This row's mutation is in flight. */
  busy?: boolean
}>(), {
  binding: null,
  busy: false,
})

const emit = defineEmits<{
  /** A dimension changed — carries the FULL merged binding for this source. */
  'set': [payload: { sourceId: string, socksEndpointId: string | null, flareMode: FlareMode, flareEndpointId: string | null }]
  /** Revert this source to the global default (delete its binding). */
  'clear': [sourceId: string]
}>()

const hasBinding = computed(() => props.binding !== null)

// The binding's current values, defaulting an unbound source to the global
// default (SOCKS none / flareMode global) — the merge base for a single-dimension
// edit so the untouched dimension survives.
const current = computed(() => ({
  socksEndpointId: props.binding?.socksEndpointId ?? null,
  flareMode: props.binding?.flareMode ?? 'global',
  flareEndpointId: props.binding?.flareEndpointId ?? null,
}))

// Append " (disabled)" so a bound-but-disabled endpoint is legible in the picker.
const epLabel = (ep: NetworkEndpoint): string => (ep.enabled ? ep.name : `${ep.name} (disabled)`)

// ── SOCKS select ────────────────────────────────────────────────────────────
const socksOptions = computed<SelectOption[]>(() => [
  { value: '', label: 'Global default' },
  ...props.socksEndpoints.map(ep => ({ value: ep.id, label: epLabel(ep) })),
])
const socksValue = computed(() => current.value.socksEndpointId ?? '')
function onSocksChange(value: string): void {
  emit('set', {
    sourceId: props.source.id,
    socksEndpointId: value === '' ? null : value,
    flareMode: current.value.flareMode,
    flareEndpointId: current.value.flareEndpointId,
  })
}

// ── FlareSolverr select ─────────────────────────────────────────────────────
// The 'none'/'global' sentinels map to flareMode; any other value is an endpoint
// id (flareMode='endpoint').
const flareOptions = computed<SelectOption[]>(() => [
  { value: 'none', label: 'None' },
  { value: 'global', label: 'Global default' },
  ...props.flareEndpoints.map(ep => ({ value: ep.id, label: epLabel(ep) })),
])
const flareValue = computed(() =>
  current.value.flareMode === 'endpoint' ? (current.value.flareEndpointId ?? 'global') : current.value.flareMode)
function onFlareChange(value: string): void {
  const flare = value === 'none'
    ? { flareMode: 'none' as FlareMode, flareEndpointId: null }
    : value === 'global'
      ? { flareMode: 'global' as FlareMode, flareEndpointId: null }
      : { flareMode: 'endpoint' as FlareMode, flareEndpointId: value }
  emit('set', {
    sourceId: props.source.id,
    socksEndpointId: current.value.socksEndpointId,
    ...flare,
  })
}
</script>

<template>
  <div class="bind-row" :class="{ 'bind-row--busy': busy, 'bind-row--bound': hasBinding }">
    <div class="bind-row__source">
      <span class="bind-row__name">{{ source.name }}</span>
      <span class="bind-row__lang">{{ source.lang }}</span>
      <span v-if="!hasBinding" class="bind-row__default-tag">Global default</span>
    </div>

    <div class="bind-row__controls">
      <label class="bind-row__control">
        <span class="bind-row__control-label">SOCKS</span>
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <SelectField :model-value="socksValue" :options="socksOptions" :disabled="busy" :ariaLabel="`SOCKS route for ${source.name}`" @update:model-value="onSocksChange" />
      </label>
      <label class="bind-row__control">
        <span class="bind-row__control-label">FlareSolverr</span>
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <SelectField :model-value="flareValue" :options="flareOptions" :disabled="busy" :ariaLabel="`FlareSolverr route for ${source.name}`" @update:model-value="onFlareChange" />
      </label>
      <div class="bind-row__clear">
        <Spinner v-if="busy" :size="14" tone="current" />
        <AppButton variant="text" size="sm" :disabled="busy || !hasBinding" @click="emit('clear', source.id)">Use global default</AppButton>
      </div>
    </div>
  </div>
</template>

<style scoped>
.bind-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  padding: 11px 13px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface2);
  margin-bottom: 9px;
}

/* A source with an explicit override gets an accent left rule so overridden
   sources stand out from the (majority) global-default rows. */
.bind-row--bound {
  border-left: 3px solid var(--accent);
}

.bind-row--busy {
  opacity: 0.6;
  pointer-events: none;
}

.bind-row__source {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.bind-row__name {
  font-weight: var(--weight-bold);
  font-size: 13.5px;
  color: var(--text);
}

.bind-row__lang {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  color: var(--faint);
}

.bind-row__default-tag {
  font-size: 9.5px;
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
  padding: 2px 7px;
  border-radius: var(--radius-pill);
  background: var(--surface3);
  color: var(--muted);
}

.bind-row__controls {
  display: flex;
  align-items: flex-end;
  gap: 12px;
  flex-wrap: wrap;
}

.bind-row__control {
  display: flex;
  flex-direction: column;
  gap: 5px;
  min-width: 150px;
}

.bind-row__control-label {
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
}

.bind-row__control :deep(.select) {
  width: 100%;
}

.bind-row__clear {
  display: flex;
  align-items: center;
  gap: 8px;
  padding-bottom: 1px;
}

@media (max-width: 900px) {
  /* Stack the source label above full-width controls on a phone (QCAT-230). */
  .bind-row__controls {
    width: 100%;
  }

  .bind-row__control {
    flex: 1;
    min-width: 130px;
  }
}
</style>
