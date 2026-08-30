<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import DurationInput from '../ui/DurationInput.vue'
import SaveFooter from '../ui/SaveFooter.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import TextField from '../ui/TextField.vue'
import NetworkPane from './NetworkPane.vue'
import SettingRow from './SettingRow.vue'
import SourceExceptionsPanel from './SourceExceptionsPanel.vue'
import SuwayomiPane from './SuwayomiPane.vue'
import type {
  FlareMode,
  FlareSolverrConfig,
  ImpersonateConfig,
  LibrarySettings,
  NetworkEndpoint,
  NetworkEndpointInput,
  RowActionState,
  SaveState,
  SourcesSettings,
} from '../screens/settings.types'
import type { components } from '../../utils/api/schema.d.ts'

type SourceIdentity = components['schemas']['SourceIdentity']
type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']
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
type ImpersonateGatewayConfig = Pick<ImpersonateConfig, 'enabled' | 'url'>

/**
 * The editable download-engine control plane. Global behavior appears first in
 * four anchored sections; the fifth section hosts the app's one complete
 * per-source editor. Engine lifecycle diagnostics and owner-triggered library
 * maintenance deliberately remain outside this component.
 */
const props = withDefaults(defineProps<{
  library: LibrarySettings
  librarySave?: SaveState
  sources: SourcesSettings
  sourcesSave?: SaveState
  flareSolverr: FlareSolverrConfig
  flareSolverrSave?: SaveState
  impersonate: ImpersonateConfig
  impersonateSave?: SaveState
  endpoints: NetworkEndpoint[]
  endpointAction?: RowActionState
  endpointsPending?: boolean
  endpointsError?: string | null
  sourceCatalog: SourceIdentity[]
  sourceSummaries: SourceExceptionSummary[]
  selectedSourceId?: string | null
  sourceConfiguration?: SourceEffectiveConfiguration | null
  sourceExceptionsPending?: boolean
  sourceConfigurationPending?: boolean
  sourceConfigurationError?: string | null
  highlightedSourceId?: string | null
  sourceAction?: {
    sourceId: string | null
    key: RowActionKey | null
    saving?: boolean
    error?: string | null
  }
  globalReuseBypassSession?: boolean
  globalImageConnectionMode?: ImageConnectionMode
}>(), {
  librarySave: () => ({ status: 'idle' }),
  sourcesSave: () => ({ status: 'idle' }),
  flareSolverrSave: () => ({ status: 'idle' }),
  impersonateSave: () => ({ status: 'idle' }),
  endpointAction: () => ({ busyId: null }),
  endpointsPending: false,
  endpointsError: null,
  selectedSourceId: null,
  sourceConfiguration: null,
  sourceExceptionsPending: false,
  sourceConfigurationPending: false,
  sourceConfigurationError: null,
  highlightedSourceId: null,
  sourceAction: () => ({ sourceId: null, key: null, saving: false, error: null }),
  globalReuseBypassSession: true,
  globalImageConnectionMode: 'reuse',
})

const emit = defineEmits<{
  'save-library': [settings: LibrarySettings]
  'save-sources': [settings: SourcesSettings]
  'save-flaresolverr': [config: FlareSolverrConfig]
  'save-impersonate': [config: ImpersonateGatewayConfig]
  'save-endpoint': [input: NetworkEndpointInput]
  'remove-endpoint': [id: string]
  'dismiss-endpoint-error': []
  'select-source': [sourceId: string]
  'set-source-override': [sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean]
  'use-global-source-setting': [sourceId: string, key: SourceConfigurationRowKey]
  'set-source-binding': [payload: { sourceId: string, socksEndpointId: string | null, flareMode: FlareMode, flareEndpointId: string | null }]
  'clear-source-binding': [sourceId: string]
}>()

const sectionLinks = [
  { id: 'download-engine-scheduling', label: 'Scheduling' },
  { id: 'download-engine-protection', label: 'Protection' },
  { id: 'download-engine-access', label: 'Access & bypass' },
  { id: 'download-engine-routing', label: 'Routing' },
  { id: 'download-engine-source-exceptions', label: 'Source exceptions' },
] as const

const cloneLibrary = (library: LibrarySettings): LibrarySettings => ({
  refreshInterval: { ...library.refreshInterval },
  downloadInterval: { ...library.downloadInterval },
  retryBackoff: { ...library.retryBackoff },
  maxRetries: library.maxRetries,
  staleGraceDays: library.staleGraceDays,
  refreshConcurrency: library.refreshConcurrency,
  downloadConcurrency: library.downloadConcurrency,
  maxConcurrentDownloads: library.maxConcurrentDownloads,
})

const libraryDraft = reactive(cloneLibrary(props.library))
watch(() => props.library, value => Object.assign(libraryDraft, cloneLibrary(value)), { deep: true })
const libraryDirty = computed(() => JSON.stringify(libraryDraft) !== JSON.stringify(props.library))
const libraryFooterState = computed(() => ({ status: props.librarySave.status, error: props.librarySave.message }))
const advancedOpen = ref(false)

const cloneSources = (sources: SourcesSettings): SourcesSettings => ({
  warmupInterval: { ...sources.warmupInterval },
  warmupSlowThresholdMs: sources.warmupSlowThresholdMs,
  failureThreshold: sources.failureThreshold,
  cooldown: { ...sources.cooldown },
  minRequestDelayMs: sources.minRequestDelayMs,
  imageRequestDelayMs: sources.imageRequestDelayMs,
})

const sourceDraft = reactive(cloneSources(props.sources))
watch(() => props.sources, value => Object.assign(sourceDraft, cloneSources(value)), { deep: true })
const sourcesDirty = computed(() => JSON.stringify(sourceDraft) !== JSON.stringify(props.sources))
const sourcesFooterState = computed(() => ({ status: props.sourcesSave.status, error: props.sourcesSave.message }))

const clampInt = (raw: string): number => Math.max(0, Number.parseInt(raw, 10) || 0)
const clampMin1 = (raw: string): number => Math.max(1, Number.parseInt(raw, 10) || 1)

function saveLibrary(): void {
  if (!libraryDirty.value || props.librarySave.status === 'saving') return
  emit('save-library', cloneLibrary(libraryDraft))
}

function saveSources(): void {
  if (!sourcesDirty.value || props.sourcesSave.status === 'saving') return
  emit('save-sources', cloneSources(sourceDraft))
}

function setSourceOverride(sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean): void {
  emit('set-source-override', sourceId, key, value)
}

function useGlobalSourceSetting(sourceId: string, key: SourceConfigurationRowKey): void {
  emit('use-global-source-setting', sourceId, key)
}
</script>

<template>
  <div class="download-engine" data-testid="download-engine-pane">
    <header class="download-engine__intro">
      <p class="download-engine__eyebrow">Download engine</p>
      <h1>Defaults first. Exceptions only where needed.</h1>
      <p>Set the shared download path once, then inspect only the sources that need different behavior.</p>
    </header>

    <div class="download-engine__layout">
      <nav class="section-spine" data-testid="engine-section-nav" aria-label="Download engine sections">
        <a v-for="link in sectionLinks" :key="link.id" :href="`#${link.id}`">
          <span aria-hidden="true" />
          {{ link.label }}
        </a>
      </nav>

      <div class="download-engine__sections">
        <section id="download-engine-scheduling" class="engine-section" data-engine-section aria-labelledby="download-engine-scheduling-title">
          <header class="engine-section__header">
            <p class="engine-section__eyebrow">Global defaults</p>
            <h2 id="download-engine-scheduling-title">Scheduling</h2>
            <p>Control discovery, queue cadence, retry budget, and shared download capacity.</p>
          </header>

          <SurfaceCard title="Cadence & capacity" sub="Schedulers re-read these values on the next tick.">
            <SettingRow name="Refresh interval" hint="How often to poll titles for new chapters">
              <DurationInput v-model="libraryDraft.refreshInterval" />
            </SettingRow>
            <SettingRow name="Download interval" hint="Queue-drain and upgrade-swap cadence">
              <DurationInput v-model="libraryDraft.downloadInterval" />
            </SettingRow>
            <SettingRow name="Chapter retry backoff" hint="Wait before retrying a failed chapter">
              <DurationInput v-model="libraryDraft.retryBackoff" />
            </SettingRow>
            <SettingRow name="Chapter max retries" hint="Attempts per source before that source is given up">
              <TextField compact type="number" :model-value="String(libraryDraft.maxRetries)" @update:model-value="libraryDraft.maxRetries = clampMin1($event)" />
            </SettingRow>
            <SettingRow name="Stale-grace days" hint="Health threshold before a source counts as stale">
              <TextField compact type="number" :model-value="String(libraryDraft.staleGraceDays)" @update:model-value="libraryDraft.staleGraceDays = clampInt($event)" />
            </SettingRow>

            <div class="advanced">
              <button type="button" class="advanced__toggle" :aria-expanded="advancedOpen" @click="advancedOpen = !advancedOpen">
                <svg class="advanced__chev" :class="{ 'advanced__chev--open': advancedOpen }" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 18l6-6-6-6" /></svg>
                Advanced capacity
              </button>
              <SettingRow v-if="advancedOpen" flush name="Refresh concurrency" hint="Parallel source fetches — be gentle on sources">
                <TextField compact type="number" :model-value="String(libraryDraft.refreshConcurrency)" @update:model-value="libraryDraft.refreshConcurrency = clampInt($event)" />
              </SettingRow>
              <SettingRow v-if="advancedOpen" flush name="Download concurrency" hint="Parallel chapter downloads allowed per source">
                <TextField compact type="number" :model-value="String(libraryDraft.downloadConcurrency)" @update:model-value="libraryDraft.downloadConcurrency = clampInt($event)" />
              </SettingRow>
              <SettingRow v-if="advancedOpen" flush name="Max concurrent downloads" hint="Global cap across every source">
                <TextField compact type="number" :model-value="String(libraryDraft.maxConcurrentDownloads)" @update:model-value="libraryDraft.maxConcurrentDownloads = clampInt($event)" />
              </SettingRow>
            </div>

            <SaveFooter :state="libraryFooterState" :dirty="libraryDirty" label="Save scheduling settings" @save="saveLibrary" />
          </SurfaceCard>

          <p class="engine-shortcut">
            One source needs a slower pace?
            <a href="#download-engine-source-exceptions" data-source-exceptions-shortcut>Set a source exception</a>.
          </p>
        </section>

        <section id="download-engine-protection" class="engine-section" data-engine-section aria-labelledby="download-engine-protection-title">
          <header class="engine-section__header">
            <p class="engine-section__eyebrow">Global defaults</p>
            <h2 id="download-engine-protection-title">Protection</h2>
            <p>Warm sessions, pause blocked sources, and pace requests before providers enforce their own limits.</p>
          </header>

          <SurfaceCard title="Anti-block protection" sub="Shared warm-up, circuit-breaker, and request pacing.">
            <SettingRow name="Warm-up interval" hint="How often to keep anti-bot source sessions warm; 0 disables">
              <DurationInput v-model="sourceDraft.warmupInterval" />
            </SettingRow>
            <SettingRow name="Warm-up slow threshold" hint="A source slower than this (ms) is treated as needing warming">
              <TextField compact type="number" :model-value="String(sourceDraft.warmupSlowThresholdMs)" @update:model-value="sourceDraft.warmupSlowThresholdMs = clampInt($event)" />
            </SettingRow>
            <SettingRow name="Failure threshold" hint="Consecutive failures before a source is paused">
              <TextField compact type="number" :model-value="String(sourceDraft.failureThreshold)" @update:model-value="sourceDraft.failureThreshold = clampMin1($event)" />
            </SettingRow>
            <SettingRow name="Source cooldown" hint="How long a failing or blocked source stays paused">
              <DurationInput v-model="sourceDraft.cooldown" />
            </SettingRow>
            <SettingRow name="Politeness delay" hint="Minimum gap (ms) between requests to one source; 0 disables">
              <TextField compact type="number" :model-value="String(sourceDraft.minRequestDelayMs)" @update:model-value="sourceDraft.minRequestDelayMs = clampInt($event)" />
            </SettingRow>
            <SettingRow name="Image request delay" hint="Global gap (ms) between individual image requests; 0 disables">
              <TextField compact type="number" :model-value="String(sourceDraft.imageRequestDelayMs)" @update:model-value="sourceDraft.imageRequestDelayMs = clampInt($event)" />
            </SettingRow>
            <SaveFooter :state="sourcesFooterState" :dirty="sourcesDirty" label="Save protection settings" @save="saveSources" />
          </SurfaceCard>

          <p class="engine-shortcut">
            A provider needs stricter protection?
            <a href="#download-engine-source-exceptions" data-source-exceptions-shortcut>Set a source exception</a>.
          </p>
        </section>

        <section id="download-engine-access" class="engine-section" data-engine-section aria-labelledby="download-engine-access-title">
          <header class="engine-section__header">
            <p class="engine-section__eyebrow">Shared services</p>
            <h2 id="download-engine-access-title">Access &amp; bypass</h2>
            <p>Configure challenge solving and the browser-fingerprint gateway once for the whole engine.</p>
          </header>

          <SuwayomiPane
            :flare-solverr="flareSolverr"
            :flare-solverr-save="flareSolverrSave"
            :impersonate="impersonate"
            :impersonate-save="impersonateSave"
            @save-flaresolverr="emit('save-flaresolverr', $event)"
            @save-impersonate="emit('save-impersonate', $event)"
          />

          <p class="engine-shortcut">
            Proxy membership and source-specific session behavior live in
            <a href="#download-engine-source-exceptions" data-source-exceptions-shortcut>Source exceptions</a>.
          </p>
        </section>

        <section id="download-engine-routing" class="engine-section" data-engine-section aria-labelledby="download-engine-routing-title">
          <header class="engine-section__header">
            <p class="engine-section__eyebrow">Reusable routes</p>
            <h2 id="download-engine-routing-title">Routing</h2>
            <p>Create SOCKS and FlareSolverr endpoints here, then assign them only where a source differs.</p>
          </header>

          <NetworkPane
            :endpoints="endpoints"
            :endpoint-action="endpointAction"
            :endpoints-pending="endpointsPending"
            :endpoints-error="endpointsError"
            @save-endpoint="emit('save-endpoint', $event)"
            @remove-endpoint="emit('remove-endpoint', $event)"
            @dismiss-endpoint-error="emit('dismiss-endpoint-error')"
          />

          <p class="engine-shortcut">
            Assign an endpoint to a source in
            <a href="#download-engine-source-exceptions" data-source-exceptions-shortcut>Source exceptions</a>.
          </p>
        </section>

        <section id="download-engine-source-exceptions" class="engine-section engine-section--exceptions" data-engine-section aria-labelledby="download-engine-source-exceptions-title">
          <header class="engine-section__header">
            <p class="engine-section__eyebrow">Per-source differences</p>
            <h2 id="download-engine-source-exceptions-title">Source exceptions</h2>
            <p>Inspect effective behavior and override only the setting that a provider requires.</p>
          </header>

          <SourceExceptionsPanel
            :sources="sourceCatalog"
            :summaries="sourceSummaries"
            :selected-source-id="selectedSourceId"
            :configuration="sourceConfiguration"
            :endpoints="endpoints"
            :global-download-concurrency="library.downloadConcurrency"
            :global-image-request-delay="`${sources.imageRequestDelayMs}ms`"
            :global-reuse-bypass-session="globalReuseBypassSession"
            :global-image-connection-mode="globalImageConnectionMode"
            :pending="sourceExceptionsPending"
            :configuration-pending="sourceConfigurationPending"
            :configuration-error="sourceConfigurationError"
            :highlighted-source-id="highlightedSourceId"
            :action="sourceAction"
            @select-source="emit('select-source', $event)"
            @set-override="setSourceOverride"
            @use-global="useGlobalSourceSetting"
            @set-binding="emit('set-source-binding', $event)"
            @clear-binding="emit('clear-source-binding', $event)"
          />
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.download-engine {
  min-width: 0;
  max-width: 100%;
}

.download-engine__intro {
  max-width: 50rem;
  margin-bottom: var(--space-2xl);
}

.download-engine__eyebrow,
.engine-section__eyebrow {
  margin: 0;
  color: var(--accentBright);
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.download-engine__intro h1 {
  margin: var(--space-3xs) 0 0;
  color: var(--text);
  font-family: var(--font-display);
  font-size: var(--text-3xl);
  line-height: 1.08;
}

.download-engine__intro > p:last-child,
.engine-section__header > p:last-child {
  margin: var(--space-xs) 0 0;
  color: var(--muted);
  line-height: 1.55;
}

.download-engine__layout {
  display: grid;
  grid-template-columns: minmax(11rem, 0.28fr) minmax(0, 1fr);
  align-items: start;
  gap: var(--space-2xl);
  min-width: 0;
}

.section-spine {
  position: sticky;
  top: var(--space-xl);
  display: grid;
  min-width: 0;
  padding-left: var(--space-lg);
  border-left: 1px solid var(--border2);
}

.section-spine a {
  position: relative;
  min-width: 0;
  padding: var(--space-sm) 0;
  color: var(--muted);
  font-size: var(--text-sm);
  font-weight: var(--weight-bold);
  text-decoration: none;
  overflow-wrap: anywhere;
}

.section-spine a:hover,
.section-spine a:focus-visible {
  color: var(--accentBright);
}

.section-spine a:focus-visible {
  border-radius: var(--radius-sm);
  outline: none;
  box-shadow: var(--ring-focus);
}

.section-spine a span {
  position: absolute;
  top: 50%;
  left: calc(-1 * var(--space-lg) - 0.3125rem);
  width: 0.625rem;
  height: 0.625rem;
  border: 2px solid var(--surface);
  border-radius: 50%;
  background: var(--border2);
  transform: translateY(-50%);
}

.section-spine a:hover span,
.section-spine a:focus-visible span {
  background: var(--accentBright);
}

.download-engine__sections {
  display: grid;
  gap: var(--space-3xl);
  min-width: 0;
  max-width: 100%;
}

.engine-section {
  display: grid;
  gap: var(--space-base);
  min-width: 0;
  max-width: 100%;
  scroll-margin-top: var(--space-xl);
}

.engine-section__header {
  min-width: 0;
  padding-left: var(--space-lg);
  border-left: 3px solid var(--accent);
}

.engine-section__header h2 {
  margin: var(--space-3xs) 0 0;
  color: var(--text);
  font-family: var(--font-display);
  font-size: var(--text-2xl);
  line-height: 1.15;
}

.engine-shortcut {
  margin: 0;
  color: var(--faint);
  font-size: var(--text-xs);
}

.engine-shortcut a {
  color: var(--accentBright);
  font-weight: var(--weight-bold);
  text-underline-offset: 0.18em;
}

.engine-shortcut a:focus-visible {
  border-radius: var(--radius-sm);
  outline: none;
  box-shadow: var(--ring-focus);
}

.advanced {
  margin-top: var(--space-3xs);
  padding-top: var(--space-sm);
  border-top: 1px solid var(--border);
}

.advanced__toggle {
  display: flex;
  align-items: center;
  gap: var(--space-xs);
  padding: 0;
  border: 0;
  background: none;
  color: var(--muted);
  font-family: var(--font-sans);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  cursor: pointer;
}

.advanced__toggle:focus-visible {
  border-radius: var(--radius-sm);
  outline: none;
  box-shadow: var(--ring-focus);
}

.advanced__chev {
  transition: transform 0.15s;
}

.advanced__chev--open {
  transform: rotate(90deg);
}

@media (max-width: 900px) {
  .download-engine__layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .section-spine {
    position: static;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 0 var(--space-base);
  }
}

@media (max-width: 560px) {
  .download-engine__intro h1 {
    font-size: var(--text-2xl);
  }

  .download-engine__layout,
  .download-engine__sections {
    gap: var(--space-xl);
  }

  .section-spine {
    grid-template-columns: minmax(0, 1fr);
  }

  .engine-section__header {
    padding-left: var(--space-base);
  }
}

@media (prefers-reduced-motion: reduce) {
  .advanced__chev {
    transition: none;
  }
}
</style>
