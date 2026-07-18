<script setup lang="ts">
import { reactive, ref, watch, computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import Dialog from '../ui/Dialog.vue'
import FormError from '../ui/FormError.vue'
import SegmentedToggle from '../ui/SegmentedToggle.vue'
import SelectField from '../ui/SelectField.vue'
import TextField from '../ui/TextField.vue'
import Toggle from '../ui/Toggle.vue'
import type { SegmentOption } from '../ui/controls.types'
import type { SelectOption } from '../ui/forms.types'
import type { NetworkEndpoint, NetworkEndpointInput, NetworkEndpointKind } from '../screens/settings.types'
import { isHttpUrl } from '~/utils/safeUrl'

/**
 * NetworkEndpointDialog — the add/edit modal for a reusable network endpoint. A
 * kind switch (SOCKS / FlareSolverr) reveals the matching field-group: SOCKS
 * (host, port, version, username, password) or FlareSolverr (url, upstream proxy,
 * session, session TTL, timeout). Submitting emits the full NetworkEndpointInput
 * (id=null for a create, the endpoint id for an update).
 *
 * SOCKS PASSWORD IS WRITE-ONLY: the backend never returns it, so on edit the
 * field opens BLANK with an "unchanged" placeholder and is sent only when the
 * owner types a new value — an empty field keeps the stored password (§16, mirrors
 * how the FlareSolverr card treats its secret). The parent owns the async
 * lifecycle: `busy` disables the form + spins Save, `error` surfaces the backend
 * message inline, and the parent closes the dialog on the success edge.
 *
 *   - `open` (v-model:open): whether the dialog is shown.
 *   - `endpoint`: the endpoint being edited, or null to add a new one.
 *   - `busy`: the save is in flight (disables the form, spins Save).
 *   - `error`: a backend save failure, surfaced inline.
 *
 * Emits `update:open` (v-model) and `submit` with the built NetworkEndpointInput.
 */
const props = withDefaults(defineProps<{
  /** Whether the dialog is open (v-model:open). */
  open: boolean
  /** The endpoint being edited, or null to add a new one. */
  endpoint?: NetworkEndpoint | null
  /** The save is in flight. */
  busy?: boolean
  /** A backend save failure, surfaced inline. */
  error?: string | null
}>(), {
  endpoint: null,
  busy: false,
  error: null,
})

const emit = defineEmits<{
  /** The open state changed (v-model:open). */
  'update:open': [value: boolean]
  /** Submit the built create/update payload. */
  'submit': [input: NetworkEndpointInput]
}>()

const KIND_OPTIONS: SegmentOption[] = [
  { key: 'socks', label: 'SOCKS' },
  { key: 'flaresolverr', label: 'FlareSolverr' },
]
const SOCKS_VERSIONS: SelectOption[] = [
  { value: '4', label: 'SOCKS4' },
  { value: '5', label: 'SOCKS5' },
]

/** A fresh editable buffer seeded from the endpoint (or the create defaults). */
function seed(ep: NetworkEndpoint | null): NetworkEndpointInput {
  if (ep) {
    return {
      id: ep.id,
      name: ep.name,
      kind: ep.kind,
      enabled: ep.enabled,
      host: ep.host,
      port: ep.port,
      socksVersion: ep.socksVersion,
      username: ep.username,
      password: '', // write-only — never pre-filled
      url: ep.url,
      session: ep.session,
      sessionTtl: ep.sessionTtl,
      timeout: ep.timeout,
      asResponseFallback: ep.asResponseFallback,
    }
  }
  return {
    id: null,
    name: '',
    kind: 'socks',
    enabled: true,
    host: '',
    port: 1080,
    socksVersion: 5,
    username: '',
    password: '',
    url: '',
    session: '',
    sessionTtl: 15,
    timeout: 60,
    asResponseFallback: true, // sensible reactive-fallback default (matches the backend)
  }
}

const form = reactive<NetworkEndpointInput>(seed(props.endpoint))
const validationError = ref('')

// Re-seed the buffer whenever the dialog (re)opens, so each open starts clean —
// the edited endpoint's values (or the create defaults), password always blank.
watch(() => props.open, (open) => {
  if (open) {
    Object.assign(form, seed(props.endpoint))
    validationError.value = ''
  }
})

const isSocks = computed(() => form.kind === 'socks')
const title = computed(() => (props.endpoint ? 'Edit endpoint' : 'Add endpoint'))
const submitLabel = computed(() => (props.endpoint ? 'Save changes' : 'Add endpoint'))
// The inline error line = local validation OR the parent's backend failure.
const errorMsg = computed(() => validationError.value || (props.error ?? ''))

// The write-only password placeholder differs between add (empty) and edit
// (keep-unchanged hint) so the owner understands leaving it blank is safe.
const passwordPlaceholder = computed(() =>
  props.endpoint ? 'Unchanged — type to replace' : 'Optional SOCKS password')

const clampInt = (raw: string, min: number): number => Math.max(min, Number.parseInt(raw, 10) || min)

function setKind(k: string): void {
  form.kind = k as NetworkEndpointKind
}

/** Validate the buffer for its kind, then emit `submit` with the full payload. */
function onSubmit(): void {
  if (props.busy) return
  const name = form.name.trim()
  if (!name) {
    validationError.value = 'Enter a name for this endpoint'
    return
  }
  if (form.kind === 'socks') {
    if (!form.host.trim()) {
      validationError.value = 'Enter the SOCKS proxy host'
      return
    }
    if (form.port < 1 || form.port > 65535) {
      validationError.value = 'Port must be between 1 and 65535'
      return
    }
  }
  else if (!isHttpUrl(form.url.trim())) {
    validationError.value = 'Enter a valid FlareSolverr URL (https://…)'
    return
  }
  validationError.value = ''
  emit('submit', { ...form, name })
}
</script>

<template>
  <Dialog :open="open" :title="title" :busy="busy" @update:open="emit('update:open', $event)">
    <div class="ep-form">
      <div class="ep-form__kind">
        <span class="ep-form__label">Endpoint type</span>
        <SegmentedToggle :model-value="form.kind" :options="KIND_OPTIONS" @update:model-value="setKind" />
      </div>

      <TextField label="Name" :model-value="form.name" :disabled="busy" @update:model-value="form.name = $event" />

      <!-- SOCKS field-group -->
      <template v-if="isSocks">
        <div class="ep-form__grid">
          <TextField label="Host" :model-value="form.host" :disabled="busy" mono @update:model-value="form.host = $event" />
          <TextField label="Port" type="number" :model-value="String(form.port)" :disabled="busy" @update:model-value="form.port = clampInt($event, 1)" />
          <div class="ep-form__field">
            <span class="ep-form__label">SOCKS version</span>
            <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
            <SelectField :model-value="String(form.socksVersion)" :options="SOCKS_VERSIONS" :disabled="busy" :ariaLabel="'SOCKS version'" @update:model-value="form.socksVersion = Number($event)" />
          </div>
          <TextField label="Username" :model-value="form.username" :disabled="busy" autocomplete="off" @update:model-value="form.username = $event" />
        </div>
        <TextField
          label="Password"
          type="password"
          :model-value="form.password ?? ''"
          :disabled="busy"
          autocomplete="off"
          :placeholder="passwordPlaceholder"
          @update:model-value="form.password = $event"
        />
      </template>

      <!-- FlareSolverr field-group (mirrors the FlareSolverr card's field set) -->
      <template v-else>
        <TextField label="Server URL" :model-value="form.url" :disabled="busy" mono @update:model-value="form.url = $event" />
        <div class="ep-form__grid">
          <TextField label="Session name" :model-value="form.session" :disabled="busy" @update:model-value="form.session = $event" />
          <TextField label="Session TTL (min)" type="number" :model-value="String(form.sessionTtl)" :disabled="busy" @update:model-value="form.sessionTtl = clampInt($event, 0)" />
          <TextField label="Timeout (s)" type="number" :model-value="String(form.timeout)" :disabled="busy" @update:model-value="form.timeout = clampInt($event, 0)" />
        </div>
        <div class="ep-form__field ep-form__field--inline">
          <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
          <Toggle :model-value="form.asResponseFallback" :disabled="busy" :ariaLabel="'Response fallback'" @update:model-value="form.asResponseFallback = $event" />
          <span class="ep-form__inline-label">Response fallback</span>
        </div>
      </template>

      <div class="ep-form__field ep-form__field--inline">
        <!-- eslint-disable-next-line vue/attribute-hyphenation -- camelCase :ariaLabel binds the REQUIRED prop; kebab :aria-label routes to the native attr, leaving it unset (vue-tsc error). -->
        <Toggle :model-value="form.enabled" :disabled="busy" :ariaLabel="'Endpoint enabled'" @update:model-value="form.enabled = $event" />
        <span class="ep-form__inline-label">Enabled</span>
      </div>

      <FormError v-if="errorMsg" :message="errorMsg" />
    </div>

    <template #actions>
      <AppButton variant="ghost" size="md" :disabled="busy" @click="emit('update:open', false)">Cancel</AppButton>
      <AppButton variant="primary" size="md" :loading="busy" @click="onSubmit">{{ submitLabel }}</AppButton>
    </template>
  </Dialog>
</template>

<style scoped>
.ep-form {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.ep-form__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.ep-form__kind,
.ep-form__field {
  display: flex;
  flex-direction: column;
}

.ep-form__field--inline {
  flex-direction: row;
  align-items: center;
  gap: 10px;
}

.ep-form__label {
  display: block;
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  text-transform: uppercase;
  letter-spacing: var(--tracking-label);
  color: var(--faint);
  margin-bottom: 6px;
}

.ep-form__inline-label {
  font-size: 12.5px;
  font-weight: var(--weight-semibold);
  color: var(--muted);
}

@media (max-width: 900px) {
  /* Two 1fr columns leave each field too narrow on a phone — stack (QCAT-230). */
  .ep-form__grid {
    grid-template-columns: 1fr;
  }
}
</style>
