<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import EmptyState from '../ui/EmptyState.vue'
import FormError from '../ui/FormError.vue'
import SearchInput from '../ui/SearchInput.vue'
import Skeleton from '../ui/Skeleton.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import SourceConfigurationGroup from './SourceConfigurationGroup.vue'
import SourceExceptionSummaryRow from './SourceExceptionSummaryRow.vue'
import type { NetworkEndpoint, SourceConfigurationRowKey } from '../screens/settings.types'
import type { components } from '../../utils/api/schema.d.ts'

type SourceIdentity = components['schemas']['SourceIdentity']
type SourceExceptionSummary = components['schemas']['SourceExceptionSummary']
type SourceEffectiveConfiguration = components['schemas']['SourceEffectiveConfiguration']
type ImageConnectionMode = components['schemas']['ImageConnectionPolicyValue']['effective']
type RowActionKey = SourceConfigurationRowKey | 'routing'

/**
 * Exception-first source overview plus one focused editor. This component owns
 * only search and provisional selection; reads, confirmed state, and writes are
 * supplied by and emitted to its host.
 */
const props = withDefaults(defineProps<{
  sources: SourceIdentity[]
  summaries: SourceExceptionSummary[]
  selectedSourceId?: string | null
  configuration?: SourceEffectiveConfiguration | null
  endpoints?: NetworkEndpoint[]
  globalDownloadConcurrency?: number
  globalImageRequestDelay?: string
  globalReuseBypassSession?: boolean
  globalImageConnectionMode?: ImageConnectionMode
  pending?: boolean
  configurationPending?: boolean
  configurationError?: string | null
  highlightedSourceId?: string | null
  highlightedSetting?: SourceConfigurationRowKey | null
  action?: {
    sourceId: string | null
    key: RowActionKey | null
    saving?: boolean
    error?: string | null
  }
  /** Semantic heading level when nested below a host section. */
  headingLevel?: 2 | 3 | 4 | 5 | 6
}>(), {
  selectedSourceId: null,
  configuration: null,
  endpoints: () => [],
  globalDownloadConcurrency: 5,
  globalImageRequestDelay: '500ms',
  globalReuseBypassSession: true,
  globalImageConnectionMode: 'reuse',
  pending: false,
  configurationPending: false,
  configurationError: null,
  highlightedSourceId: null,
  highlightedSetting: null,
  action: () => ({ sourceId: null, key: null, saving: false, error: null }),
  headingLevel: 2,
})

const emit = defineEmits<{
  'select-source': [sourceId: string]
  'set-override': [sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean]
  'use-global': [sourceId: string, key: SourceConfigurationRowKey]
  'set-binding': [payload: { sourceId: string, socksEndpointId: string | null, flareMode: 'none' | 'global' | 'endpoint', flareEndpointId: string | null }]
  'clear-binding': [sourceId: string]
}>()

const query = ref('')
const localSelectedSourceId = ref<string | null>(props.selectedSourceId)

watch(() => props.selectedSourceId, sourceId => {
  localSelectedSourceId.value = sourceId
})

const normalizedQuery = computed(() => query.value.trim().toLocaleLowerCase())
const summaryBySource = computed(() => new Map(props.summaries.map(summary => [summary.source.sourceId, summary])))
const sourceById = computed(() => new Map(props.sources.map(source => [source.sourceId, source])))
const exceptionSourceCount = computed(() => props.summaries.length)
const explicitSettingCount = computed(() => props.summaries.reduce((total, summary) => total + summary.exceptionCount, 0))
const pendingApplyCount = computed(() => props.summaries.filter(summary => summary.runtime.status === 'pending').length)

const visibleSources = computed(() => {
  if (normalizedQuery.value) {
    const sources = props.sources.filter(source => `${source.name} ${source.language}`.toLocaleLowerCase().includes(normalizedQuery.value))
    const highlighted = props.highlightedSourceId ? sourceById.value.get(props.highlightedSourceId) : null
    if (highlighted && !sources.some(source => source.sourceId === highlighted.sourceId)) sources.push(highlighted)
    return sources
  }

  const sources = props.summaries.map(summary => summary.source)
  for (const sourceId of [localSelectedSourceId.value, props.highlightedSourceId]) {
    if (!sourceId) continue
    if (sources.some(source => source.sourceId === sourceId)) continue
    const source = sourceById.value.get(sourceId)
    if (source) sources.push(source)
  }
  return sources
})

const currentConfiguration = computed(() => {
  if (props.configuration?.source.sourceId !== localSelectedSourceId.value) return null
  return props.configuration
})

function selectSource(sourceId: string): void {
  localSelectedSourceId.value = sourceId
  emit('select-source', sourceId)
}

function forwardSetOverride(sourceId: string, key: SourceConfigurationRowKey, value: string | number | boolean): void {
  emit('set-override', sourceId, key, value)
}

function forwardUseGlobal(sourceId: string, key: SourceConfigurationRowKey): void {
  emit('use-global', sourceId, key)
}
</script>

<template>
  <SurfaceCard
    class="source-exceptions"
    :heading-level="headingLevel"
    title="Source exceptions"
    sub="Start with sources that differ from global behavior, then search the full installed catalog."
  >
    <div v-if="pending" class="source-exceptions__loading" role="status" aria-label="Loading source exceptions">
      <Skeleton variant="line" height="42px" />
      <div class="source-exceptions__workspace">
        <div class="source-exceptions__rail-loading">
          <Skeleton v-for="n in 3" :key="n" variant="row" />
        </div>
        <Skeleton variant="card" height="28rem" />
      </div>
    </div>

    <EmptyState
      v-else-if="sources.length === 0"
      title="No sources installed"
      sub="Install a source extension before adding source-specific settings."
    />

    <template v-else>
      <div class="source-exceptions__counts" aria-label="Source exception overview">
        <div data-testid="exception-source-count">
          <strong>{{ exceptionSourceCount }}</strong>
          <span>Exception {{ exceptionSourceCount === 1 ? 'source' : 'sources' }}</span>
        </div>
        <div data-testid="explicit-setting-count">
          <strong>{{ explicitSettingCount }}</strong>
          <span>Explicit {{ explicitSettingCount === 1 ? 'setting' : 'settings' }}</span>
        </div>
        <div data-testid="pending-apply-count">
          <strong>{{ pendingApplyCount }}</strong>
          <span>Pending {{ pendingApplyCount === 1 ? 'apply' : 'applies' }}</span>
        </div>
      </div>

      <div class="source-exceptions__workspace">
        <aside class="source-exceptions__rail" aria-label="Sources">
          <SearchInput v-model="query" label="Search installed sources" placeholder="Search every installed source" />
          <p v-if="!normalizedQuery" class="source-exceptions__rail-label">Exceptions first</p>
          <p v-else class="source-exceptions__rail-label">Catalog results</p>

          <div v-if="visibleSources.length" class="source-exceptions__rows">
            <SourceExceptionSummaryRow
              v-for="source in visibleSources"
              :key="source.sourceId"
              :source="source"
              :exception-count="summaryBySource.get(source.sourceId)?.exceptionCount ?? 0"
              :runtime="summaryBySource.get(source.sourceId)?.runtime ?? null"
              :selected="source.sourceId === localSelectedSourceId"
              :highlighted="source.sourceId === highlightedSourceId && highlightedSetting == null"
              @select="selectSource"
            />
          </div>

          <p v-else-if="normalizedQuery" class="source-exceptions__empty-result">
            No sources match “{{ query.trim() }}”.
          </p>
          <p v-else class="source-exceptions__empty-result">
            Every source currently inherits the global settings. Search the catalog to inspect one.
          </p>
        </aside>

        <main class="source-exceptions__editor" aria-live="polite">
          <div v-if="configurationPending" role="status" aria-label="Loading source configuration">
            <Skeleton variant="card" height="28rem" />
          </div>
          <FormError v-else-if="configurationError" :message="configurationError" />
          <SourceConfigurationGroup
            v-else-if="currentConfiguration"
            :key="currentConfiguration.source.sourceId"
            :configuration="currentConfiguration"
            :endpoints="endpoints"
            :global-download-concurrency="globalDownloadConcurrency"
            :global-image-request-delay="globalImageRequestDelay"
            :global-reuse-bypass-session="globalReuseBypassSession"
            :global-image-connection-mode="globalImageConnectionMode"
            :action="action"
            :highlighted-setting="highlightedSetting"
            @set-override="forwardSetOverride"
            @use-global="forwardUseGlobal"
            @set-binding="emit('set-binding', $event)"
            @clear-binding="emit('clear-binding', $event)"
          />
          <div v-else class="source-exceptions__editor-empty">
            <h3>{{ localSelectedSourceId ? 'Loading selected source' : 'Choose a source' }}</h3>
            <p>{{ localSelectedSourceId ? 'Its effective configuration will appear here.' : 'Select an exception or search the catalog to inspect effective behavior.' }}</p>
          </div>
        </main>
      </div>
    </template>
  </SurfaceCard>
</template>

<style scoped>
.source-exceptions {
  min-width: 0;
  max-width: 100%;
}

.source-exceptions__loading {
  display: grid;
  gap: var(--space-base);
}

.source-exceptions__counts {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  min-width: 0;
  margin: var(--space-sm) 0 var(--space-lg);
  border-block: 1px solid var(--border);
}

.source-exceptions__counts div {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: baseline;
  gap: var(--space-xs);
  min-width: 0;
  padding: var(--space-base);
  border-right: 1px solid var(--border);
}

.source-exceptions__counts div:last-child {
  border-right: 0;
}

.source-exceptions__counts strong {
  color: var(--accentBright);
  font-family: var(--font-display);
  font-size: var(--text-2xl);
  font-weight: var(--weight-bold);
}

.source-exceptions__counts span {
  min-width: 0;
  color: var(--muted);
  font-size: var(--text-xs);
  font-weight: var(--weight-bold);
  overflow-wrap: anywhere;
}

.source-exceptions__workspace {
  display: grid;
  grid-template-columns: minmax(15rem, 0.72fr) minmax(0, 1.65fr);
  gap: var(--space-xl);
  min-width: 0;
  max-width: 100%;
}

.source-exceptions__rail,
.source-exceptions__editor,
.source-exceptions__rail-loading {
  min-width: 0;
  max-width: 100%;
}

.source-exceptions__rail {
  align-self: start;
  padding-right: var(--space-lg);
  border-right: 1px solid var(--border);
}

.source-exceptions__rail-label {
  margin: var(--space-base) var(--space-base) var(--space-xs);
  color: var(--faint);
  font-size: var(--text-2xs);
  font-weight: var(--weight-extrabold);
  letter-spacing: var(--tracking-label);
  text-transform: uppercase;
}

.source-exceptions__rows,
.source-exceptions__rail-loading {
  display: grid;
  gap: var(--space-3xs);
}

.source-exceptions__empty-result {
  margin: var(--space-lg) var(--space-base);
  color: var(--muted);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.source-exceptions__editor-empty {
  display: grid;
  place-content: center;
  min-height: 18rem;
  padding: var(--space-xl);
  border: 1px dashed var(--border2);
  border-radius: var(--radius-xl);
  text-align: center;
}

.source-exceptions__editor-empty h3 {
  margin: 0;
  color: var(--text);
  font-family: var(--font-display);
  font-size: var(--text-lg);
}

.source-exceptions__editor-empty p {
  max-width: 28rem;
  margin: var(--space-xs) 0 0;
  color: var(--muted);
  font-size: var(--text-sm);
}

@media (max-width: 900px) {
  .source-exceptions__workspace {
    grid-template-columns: minmax(0, 1fr);
  }

  .source-exceptions__rail {
    padding: 0 0 var(--space-lg);
    border-right: 0;
    border-bottom: 1px solid var(--border);
  }
}

@media (max-width: 560px) {
  .source-exceptions__counts {
    grid-template-columns: minmax(0, 1fr);
  }

  .source-exceptions__counts div {
    border-right: 0;
    border-bottom: 1px solid var(--border);
  }

  .source-exceptions__counts div:last-child {
    border-bottom: 0;
  }
}
</style>
