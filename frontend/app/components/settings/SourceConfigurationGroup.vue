<script setup lang="ts">
import { computed } from 'vue'
import FormError from '../ui/FormError.vue'
import SourceApplyStatus from './SourceApplyStatus.vue'
import SourceBindingRow from './SourceBindingRow.vue'
import SourceOverrideRow from './SourceOverrideRow.vue'
import SourceProxyOptInRow from './SourceProxyOptInRow.vue'
import type { NetworkEndpoint, SourceBinding } from '../screens/settings.types'
import type { components } from '../../utils/api/schema.d.ts'

type SourceEffectiveConfiguration = components['schemas']['SourceEffectiveConfiguration']
type ImageConnectionMode = components['schemas']['ImageConnectionPolicyValue']['effective']
type SourceConfigurationRowKey =
  | 'downloadConcurrency'
  | 'imageRequestDelay'
  | 'byparr'
  | 'reuseBypassSession'
  | 'imageConnectionMode'
  | 'imageProxy'
  | 'socksBinding'
  | 'bypassBinding'
type RowActionKey = SourceConfigurationRowKey | 'routing'

/**
 * The app's one complete source-configuration editor. It composes the existing
 * row controls and emits persistence intent; confirmed values and row-local
 * action state remain parent-owned.
 */
const props = withDefaults(defineProps<{
  configuration: SourceEffectiveConfiguration
  endpoints?: NetworkEndpoint[]
  globalDownloadConcurrency?: number
  globalImageRequestDelay?: string
  globalReuseBypassSession?: boolean
  globalImageConnectionMode?: ImageConnectionMode
  action?: {
    sourceId: string | null
    key: RowActionKey | null
    saving?: boolean
    error?: string | null
  }
}>(), {
  endpoints: () => [],
  globalDownloadConcurrency: 5,
  globalImageRequestDelay: '500ms',
  globalReuseBypassSession: true,
  globalImageConnectionMode: 'reuse',
  action: () => ({ sourceId: null, key: null, saving: false, error: null }),
})

const emit = defineEmits<{
  'set-override': [sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean]
  'use-global': [sourceId: string, key: SourceConfigurationRowKey]
  'set-binding': [payload: { sourceId: string, socksEndpointId: string | null, flareMode: 'none' | 'global' | 'endpoint', flareEndpointId: string | null }]
  'clear-binding': [sourceId: string]
}>()

const sourceId = computed(() => props.configuration.source.sourceId)
const statusRuntime = computed(() => ({
  ...props.configuration.runtime,
  // The diagnostic is deliberately revealed only by Advanced diagnostics.
  lastApplyError: '',
}))
const socksEndpoints = computed(() => props.endpoints.filter(endpoint => endpoint.kind === 'socks'))
const flareEndpoints = computed(() => props.endpoints.filter(endpoint => endpoint.kind === 'flaresolverr'))
const source = computed(() => ({
  id: sourceId.value,
  name: props.configuration.source.name,
  lang: props.configuration.source.language,
}))
const binding = computed<SourceBinding | null>(() => {
  const routing = props.configuration.routing
  if (routing.socksMode === 'global' && routing.bypassMode === 'global') return null
  return {
    sourceId: sourceId.value,
    socksEndpointId: routing.socksMode === 'endpoint' ? routing.socks.endpointId : null,
    flareMode: routing.bypassMode,
    flareEndpointId: routing.bypassMode === 'endpoint' ? routing.bypass.endpointId : null,
  }
})

function rowSaving(key: RowActionKey): boolean {
  return props.action.sourceId === sourceId.value && props.action.key === key && Boolean(props.action.saving)
}

function rowError(key: RowActionKey): string | null {
  if (props.action.sourceId !== sourceId.value || props.action.key !== key) return null
  return props.action.error ?? null
}

function setOverride(key: SourceConfigurationRowKey, value: string | number | boolean): void {
  emit('set-override', sourceId.value, key, value)
}

function useGlobal(key: SourceConfigurationRowKey): void {
  emit('use-global', sourceId.value, key)
}
</script>

<template>
  <section class="configuration" data-testid="source-editor" :aria-labelledby="`source-configuration-${sourceId}`">
    <header class="configuration__header">
      <div class="configuration__heading">
        <p class="configuration__eyebrow">Focused source</p>
        <h3 :id="`source-configuration-${sourceId}`">{{ configuration.source.name }}</h3>
        <span class="configuration__language">{{ configuration.source.language }}</span>
      </div>
      <SourceApplyStatus :runtime="statusRuntime" />
    </header>

    <div class="configuration__section">
      <div class="configuration__section-head">
        <h4>Download pace</h4>
        <p>Effective values are always shown; overrides apply only to this source.</p>
      </div>
      <SourceOverrideRow
        setting-key="downloadConcurrency"
        name="Chapter concurrency"
        hint="Maximum chapter downloads this source may run at once"
        control="number"
        :model-value="configuration.downloadConcurrency.effective"
        :global-value="globalDownloadConcurrency"
        :inherited="configuration.downloadConcurrency.inherited"
        :saving="rowSaving('downloadConcurrency')"
        :error="rowError('downloadConcurrency')"
        @set-override="setOverride"
        @use-global="useGlobal"
      />
      <SourceOverrideRow
        setting-key="imageRequestDelay"
        name="Image request delay"
        hint="Gap between individual image requests; 0s disables pacing"
        control="text"
        :model-value="configuration.imageRequestDelay.effective"
        :global-value="globalImageRequestDelay"
        :inherited="configuration.imageRequestDelay.inherited"
        :saving="rowSaving('imageRequestDelay')"
        :error="rowError('imageRequestDelay')"
        @set-override="setOverride"
        @use-global="useGlobal"
      />
    </div>

    <div class="configuration__section">
      <div class="configuration__section-head">
        <h4>Protection in effect</h4>
        <p>Read-only global protection values currently applied to this source.</p>
      </div>
      <dl class="configuration__facts">
        <div><dt>Warm-up interval</dt><dd>{{ configuration.protection.warmupInterval }}</dd></div>
        <div><dt>Slow threshold</dt><dd>{{ configuration.protection.warmupSlowThresholdMs }} ms</dd></div>
        <div><dt>Failure threshold</dt><dd>{{ configuration.protection.failureThreshold }}</dd></div>
        <div><dt>Source cooldown</dt><dd>{{ configuration.protection.sourceCooldown }}</dd></div>
        <div><dt>Politeness delay</dt><dd>{{ configuration.protection.politenessDelay }}</dd></div>
        <div><dt>Bypass service</dt><dd>{{ configuration.bypassEnabled ? 'Enabled' : 'Disabled' }}</dd></div>
      </dl>
    </div>

    <div class="configuration__section">
      <div class="configuration__section-head">
        <h4>Sessions and images</h4>
        <p>Keep inherited transport behavior unless this source needs isolation.</p>
      </div>
      <SourceOverrideRow
        setting-key="reuseBypassSession"
        name="Reuse bypass session"
        :hint="`Effective mode: ${configuration.reuseBypassSession.mode}`"
        control="toggle"
        :model-value="configuration.reuseBypassSession.effective"
        :global-value="globalReuseBypassSession"
        :inherited="configuration.reuseBypassSession.inherited"
        :saving="rowSaving('reuseBypassSession')"
        :error="rowError('reuseBypassSession')"
        @set-override="setOverride"
        @use-global="useGlobal"
      />
      <SourceOverrideRow
        setting-key="imageConnectionMode"
        name="Image connection"
        hint="Reuse a connection or open a fresh one for each image"
        control="select"
        :model-value="configuration.imageConnectionMode.effective"
        :global-value="globalImageConnectionMode"
        :inherited="configuration.imageConnectionMode.inherited"
        :options="[
          { value: 'reuse', label: 'Reuse connection' },
          { value: 'fresh', label: 'Fresh connection per image' },
        ]"
        :saving="rowSaving('imageConnectionMode')"
        :error="rowError('imageConnectionMode')"
        @set-override="setOverride"
        @use-global="useGlobal"
      />
      <SourceProxyOptInRow
        :enabled="configuration.imageProxy.optedIn"
        :effective-available="configuration.imageProxy.effectiveAvailable"
        :saving="rowSaving('imageProxy')"
        :error="rowError('imageProxy')"
        @set-override="setOverride"
      />
      <dl class="configuration__facts configuration__facts--compact">
        <div><dt>Gateway enabled</dt><dd>{{ configuration.imageProxy.gatewayEnabled ? 'Yes' : 'No' }}</dd></div>
        <div><dt>Gateway configured</dt><dd>{{ configuration.imageProxy.gatewayConfigured ? 'Yes' : 'No' }}</dd></div>
      </dl>
    </div>

    <div class="configuration__section">
      <div class="configuration__section-head">
        <h4>Routing</h4>
        <p>Choose reusable endpoints without leaving this source editor.</p>
      </div>
      <SourceBindingRow
        :source="source"
        :binding="binding"
        :socks-endpoints="socksEndpoints"
        :flare-endpoints="flareEndpoints"
        :busy="rowSaving('routing')"
        @set="emit('set-binding', $event)"
        @clear="emit('clear-binding', $event)"
      />
      <FormError v-if="rowError('routing')" :message="rowError('routing')!" />
    </div>

    <details class="configuration__diagnostics">
      <summary>Advanced diagnostics</summary>
      <dl class="configuration__facts configuration__facts--diagnostics">
        <div><dt>Profile key</dt><dd>{{ configuration.profileKey }}</dd></div>
        <div><dt>Desired revision</dt><dd>{{ configuration.runtime.desiredRevision }}</dd></div>
        <div><dt>Applied revision</dt><dd>{{ configuration.runtime.appliedRevision }}</dd></div>
        <div><dt>Status</dt><dd>{{ configuration.runtime.status }}</dd></div>
        <div><dt>Last attempt</dt><dd>{{ configuration.runtime.lastApplyAttempt ?? 'Never' }}</dd></div>
        <div><dt>Sanitized error</dt><dd>{{ configuration.runtime.lastApplyError || 'None' }}</dd></div>
      </dl>
    </details>
  </section>
</template>

<style scoped>
.configuration {
  display: grid;
  gap: var(--space-lg);
  min-width: 0;
  max-width: 100%;
}

.configuration__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-base);
  min-width: 0;
  padding-bottom: var(--space-base);
  border-bottom: 1px solid var(--border);
}

.configuration__heading {
  display: grid;
  grid-template-columns: minmax(0, auto) auto;
  align-items: baseline;
  gap: var(--space-3xs) var(--space-xs);
  min-width: 0;
}

.configuration__eyebrow {
  grid-column: 1 / -1;
  margin: 0;
  color: var(--accentBright);
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.configuration__heading h3,
.configuration__section-head h4 {
  margin: 0;
  color: var(--text);
  font-family: var(--font-display);
  font-weight: var(--weight-bold);
}

.configuration__heading h3 {
  min-width: 0;
  font-size: var(--text-xl);
  overflow-wrap: anywhere;
}

.configuration__language {
  color: var(--faint);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
  text-transform: uppercase;
}

.configuration__section {
  display: grid;
  gap: var(--space-sm);
  min-width: 0;
  max-width: 100%;
}

.configuration__section-head h4 {
  font-size: var(--text-base);
}

.configuration__section-head p {
  margin: var(--space-3xs) 0 0;
  color: var(--faint);
  font-size: var(--text-xs);
}

.configuration__facts {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1px;
  min-width: 0;
  margin: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  overflow: hidden;
  background: var(--border);
}

.configuration__facts div {
  display: grid;
  gap: var(--space-3xs);
  min-width: 0;
  padding: var(--space-sm);
  background: var(--surface2);
}

.configuration__facts dt {
  color: var(--faint);
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.configuration__facts dd {
  min-width: 0;
  margin: 0;
  color: var(--text);
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.configuration__facts--compact {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.configuration__diagnostics {
  min-width: 0;
  border-top: 1px solid var(--border);
  padding-top: var(--space-base);
}

.configuration__diagnostics summary {
  width: fit-content;
  border-radius: var(--radius-sm);
  color: var(--muted);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.configuration__diagnostics summary:focus-visible {
  outline: none;
  box-shadow: var(--ring-focus);
}

.configuration__facts--diagnostics {
  margin-top: var(--space-sm);
}

@media (max-width: 900px) {
  .configuration__facts {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 560px) {
  .configuration__header {
    flex-direction: column;
  }

  .configuration__facts,
  .configuration__facts--compact {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
