<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppButton from '../ui/AppButton.vue'
import ErrorBanner from '../ui/ErrorBanner.vue'
import FormError from '../ui/FormError.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import NetworkEndpointRow from './NetworkEndpointRow.vue'
import NetworkEndpointDialog from './NetworkEndpointDialog.vue'
import SourceBindingRow from './SourceBindingRow.vue'
import {
  ADD_ACTION_ID,
  type FlareMode,
  type NetworkEndpoint,
  type NetworkEndpointInput,
  type NetworkSource,
  type RowActionState,
  type SourceBinding,
} from '../screens/settings.types'

/**
 * NetworkPane — the "Network" settings pane: two stacked cards for per-source
 * network routing (QCAT-283). Card 1 is the reusable-endpoint manager
 * (add / edit / remove SOCKS + FlareSolverr endpoints via a dialog); card 2 is
 * the per-source assignment table (a SOCKS + FlareSolverr route select per engine
 * source, with a "use global default" clear).
 *
 * Presentation-only: ALL data arrives via props and every mutation is emitted —
 * the composables live in the page. The pane owns only local UI state: which
 * endpoint the editor dialog is editing, and it closes that dialog on the save
 * success edge (`endpointAction.busyId` clears with no error), mirroring how
 * CategoriesPane/ExtensionsPane close their modals. All §16 states are visible:
 * per-card loading skeletons, inline load errors, the endpoint save error inside
 * the dialog, the delete-in-use 409 as a dismissible card banner (its message
 * lists the referencing sources), and the binding error inline.
 */
const props = withDefaults(defineProps<{
  /** The defined egress endpoints. */
  endpoints: NetworkEndpoint[]
  /** §16 state of the endpoint save/delete mutation (busy row + error). */
  endpointAction?: RowActionState
  /** Whether the endpoint list is loading. */
  endpointsPending?: boolean
  /** An endpoint-list load failure, surfaced inline. */
  endpointsError?: string | null
  /** The engine sources (assignment table rows). */
  sources: NetworkSource[]
  /** The per-source bindings (a source absent here uses the global default). */
  bindings: SourceBinding[]
  /** §16 state of the binding set/clear mutation (busy source + error). */
  bindingAction?: RowActionState
  /** Whether the sources/bindings are loading. */
  bindingsPending?: boolean
  /** A sources/bindings load failure, surfaced inline. */
  bindingsError?: string | null
}>(), {
  endpointAction: () => ({ busyId: null }),
  endpointsPending: false,
  endpointsError: null,
  bindingAction: () => ({ busyId: null }),
  bindingsPending: false,
  bindingsError: null,
})

const emit = defineEmits<{
  /** Create or update an endpoint (id=null = create). */
  'save-endpoint': [input: NetworkEndpointInput]
  /** Remove an endpoint by id. */
  'remove-endpoint': [id: string]
  /** Dismiss the lingering endpoint-action error banner. */
  'dismiss-endpoint-error': []
  /** Set (upsert) a source's binding — carries the full merged binding. */
  'set-binding': [payload: { sourceId: string, socksEndpointId: string | null, flareMode: FlareMode, flareEndpointId: string | null }]
  /** Clear a source's binding (revert to global default). */
  'clear-binding': [sourceId: string]
}>()

// ── Endpoint editor dialog (local UI state) ──────────────────────────────────
const dialogOpen = ref(false)
const editing = ref<NetworkEndpoint | null>(null)

function openAdd(): void {
  editing.value = null
  dialogOpen.value = true
}
function openEdit(ep: NetworkEndpoint): void {
  editing.value = ep
  dialogOpen.value = true
}
function onSubmitEndpoint(input: NetworkEndpointInput): void {
  emit('save-endpoint', input)
  // No async wiring (Storybook) → the busy watcher never fires, so the dialog
  // simply stays open showing the submitted state; with a real composable the
  // watcher below closes it on the success edge.
}

// Close the dialog on the save success edge: the save's busyId (the endpoint id
// or ADD_ACTION_ID) clears back to null with NO error. A save error keeps the
// dialog open so its inline FormError is visible.
watch(() => props.endpointAction.busyId, (busyId, prev) => {
  if (!dialogOpen.value) return
  const wasSaving = prev === (editing.value?.id ?? ADD_ACTION_ID)
  if (wasSaving && busyId == null && !props.endpointAction.error) {
    dialogOpen.value = false
    editing.value = null
  }
})

// The dialog owns the save error while open; the card banner owns the delete
// (in-use 409) error while the dialog is closed — they never overlap.
const dialogError = computed(() => (dialogOpen.value ? props.endpointAction.error ?? null : null))
const cardError = computed(() => (dialogOpen.value ? '' : props.endpointAction.error ?? ''))

// Per-row endpoint busy (the single in-flight endpoint id).
const endpointRowBusy = (id: string): boolean => props.endpointAction.busyId === id

// ── Per-source assignment table ──────────────────────────────────────────────
const socksEndpoints = computed(() => props.endpoints.filter(ep => ep.kind === 'socks'))
const flareEndpoints = computed(() => props.endpoints.filter(ep => ep.kind === 'flaresolverr'))

// Fast lookup of a source's binding (absent = unbound = global default).
const bindingBySource = computed(() => {
  const map = new Map<string, SourceBinding>()
  for (const b of props.bindings) map.set(b.sourceId, b)
  return map
})
const bindingFor = (sourceId: string): SourceBinding | null => bindingBySource.value.get(sourceId) ?? null
const bindingRowBusy = (sourceId: string): boolean => props.bindingAction.busyId === sourceId

const endpointSkeletons = [0, 1]
const bindingSkeletons = [0, 1, 2]
</script>

<template>
  <div class="pane-stack">
    <!-- Card 1 — reusable egress endpoints ------------------------------------ -->
    <SurfaceCard
      title="Egress endpoints"
      sub="Reusable SOCKS proxies + FlareSolverr servers you can route individual sources through."
    >
      <template #actions>
        <AppButton variant="mini" size="sm" @click="openAdd">
          <template #icon>
            <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.6" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14" /></svg>
          </template>
          Add endpoint
        </AppButton>
      </template>

      <!-- §16 delete/in-use (409) failure — its message lists the referencing sources. -->
      <div v-if="cardError" class="net-banner">
        <ErrorBanner :message="cardError" @dismiss="emit('dismiss-endpoint-error')" />
      </div>

      <!-- Loading skeletons -->
      <div v-if="endpointsPending" class="net-list">
        <div v-for="n in endpointSkeletons" :key="n" class="skeleton-row" />
      </div>

      <!-- Load error -->
      <div v-else-if="endpointsError" class="net-load-error">
        <FormError :message="endpointsError" />
      </div>

      <!-- Empty -->
      <p v-else-if="endpoints.length === 0" class="net-empty">
        No endpoints yet — add a SOCKS proxy or FlareSolverr server to route sources through.
      </p>

      <!-- The endpoint rows -->
      <div v-else>
        <NetworkEndpointRow
          v-for="ep in endpoints"
          :key="ep.id"
          :endpoint="ep"
          :busy="endpointRowBusy(ep.id)"
          @edit="openEdit(ep)"
          @remove="emit('remove-endpoint', ep.id)"
        />
      </div>
    </SurfaceCard>

    <!-- Card 2 — per-source assignment table ----------------------------------- -->
    <SurfaceCard
      title="Per-source routing"
      sub="Route each source through an endpoint, or leave it on the global default. Unbound sources use the global SOCKS + FlareSolverr config."
    >
      <!-- §16 binding set/clear failure. -->
      <div v-if="bindingAction.error" class="net-binding-error">
        <FormError :message="bindingAction.error" />
      </div>

      <!-- Loading skeletons -->
      <div v-if="bindingsPending" class="net-list">
        <div v-for="n in bindingSkeletons" :key="n" class="skeleton-row" />
      </div>

      <!-- Load error -->
      <div v-else-if="bindingsError" class="net-load-error">
        <FormError :message="bindingsError" />
      </div>

      <!-- Empty -->
      <p v-else-if="sources.length === 0" class="net-empty">
        No sources installed yet — install a source extension to assign a route.
      </p>

      <!-- The source rows -->
      <div v-else>
        <SourceBindingRow
          v-for="s in sources"
          :key="s.id"
          :source="s"
          :binding="bindingFor(s.id)"
          :socks-endpoints="socksEndpoints"
          :flare-endpoints="flareEndpoints"
          :busy="bindingRowBusy(s.id)"
          @set="emit('set-binding', $event)"
          @clear="emit('clear-binding', $event)"
        />
      </div>
    </SurfaceCard>

    <!-- Add / edit endpoint dialog. -->
    <NetworkEndpointDialog
      v-model:open="dialogOpen"
      :endpoint="editing"
      :busy="endpointAction.busyId === (editing?.id ?? ADD_ACTION_ID)"
      :error="dialogError"
      @submit="onSubmitEndpoint"
    />
  </div>
</template>

<style scoped>
/* Two stacked cards with the shared 16px inter-card rhythm — same shape as the
   Sources pane's own pane-stack. */
.pane-stack {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.net-banner {
  margin-bottom: 12px;
}

.net-binding-error,
.net-load-error {
  margin-bottom: 12px;
}

.net-empty {
  padding: 14px 2px;
  font-size: var(--text-sm);
  color: var(--muted);
}

.net-list {
  display: flex;
  flex-direction: column;
  gap: 9px;
}

/* ---- Loading skeletons ---------------------------------------------------- */
.skeleton-row {
  height: 54px;
  border-radius: var(--radius-lg);
  background: var(--surface2);
  position: relative;
  overflow: hidden;
}

.skeleton-row::after {
  content: '';
  position: absolute;
  inset: 0;
  transform: translateX(-100%);
  background: linear-gradient(90deg, transparent, var(--surface3), transparent);
  animation: net-shimmer 1.4s ease-in-out infinite;
}

@keyframes net-shimmer {
  to { transform: translateX(100%); }
}
</style>
