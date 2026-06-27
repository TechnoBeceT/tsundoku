<script setup lang="ts">
import { computed } from 'vue'
import AppButton from '../ui/AppButton.vue'
import LockedRow from '../ui/LockedRow.vue'
import Stepper from '../ui/Stepper.vue'
import SurfaceCard from '../ui/SurfaceCard.vue'
import SettingRow from './SettingRow.vue'
import type { StepItem } from '../ui/nav.types'
import type { EngineInfo, UpgradeStep } from '../screens/settings.types'

/**
 * EnginePane — read-only Suwayomi engine status. In external mode it shows only
 * the external URL; in embedded mode it shows the running/pinned version, the
 * runtime diagnostic rows, and the upgrade affordance (an "up to date" marker, an
 * "upgrade available" CTA, and — once an upgrade is running — the vertical
 * progress Stepper, SSE-driven via `upgradeSteps`).
 *
 *   - `engine`: the read-only engine status (mode drives which rows show).
 *   - `upgradeSteps`: the upgrade stepper's steps; empty = no upgrade started.
 *   - `upgrading`: whether an upgrade call is currently in flight.
 *
 * Emits `start-upgrade` when the owner kicks off the embedded-engine upgrade.
 */
const props = withDefaults(defineProps<{
  /** The read-only engine status. */
  engine: EngineInfo
  /** The upgrade stepper's steps (SSE-driven); empty = no upgrade running. */
  upgradeSteps?: UpgradeStep[]
  /** Whether an upgrade is currently running (disables the CTA). */
  upgrading?: boolean
}>(), {
  upgradeSteps: () => [],
  upgrading: false,
})

const emit = defineEmits<{
  /** Start the embedded-engine upgrade flow. */
  'start-upgrade': []
}>()

const upgradeShown = computed(() => props.upgradeSteps.length > 0)

// The header sub-line depends on the lifecycle mode (external = unmanaged).
const headerSub = computed(() =>
  props.engine.mode === 'external'
    ? 'Pointing at an external instance — Tsundoku does not manage its lifecycle.'
    : 'Tsundoku provisions and runs its own engine JAR.')

// Adapt the per-step status array to the Stepper atom's {steps, current} shape:
// the atom paints everything before `current` done, the match active, the rest
// todo — which reproduces the monotonic Stop → Backup → … sequence. `current` is
// the in-flight step (the first one not yet done).
const stepperSteps = computed<StepItem[]>(() =>
  props.upgradeSteps.map(s => ({ key: s.label, label: s.label })),
)
const currentStep = computed(() => {
  const active = props.upgradeSteps.find((s: UpgradeStep) => s.status !== 'done')
  return active?.label ?? ''
})

function startUpgrade() {
  if (props.upgrading) return
  emit('start-upgrade')
}
</script>

<template>
  <SurfaceCard title="Suwayomi engine" :sub="headerSub">
    <template #actions>
      <span class="mode-badge">{{ engine.mode === 'embedded' ? 'Embedded' : 'External' }}</span>
    </template>

    <template v-if="engine.mode === 'external'">
      <LockedRow label="External URL" :value="engine.externalUrl" />
    </template>

    <template v-else>
      <SettingRow name="Running version" :hint="`pinned target ${engine.pinnedVersion}`">
        <div class="status-line">
          <span class="status-dot" />
          <span class="status-text">{{ engine.status }}</span>
          <span class="mono">{{ engine.runningVersion }}</span>
        </div>
      </SettingRow>
      <LockedRow label="Runtime dir" :value="engine.runtimeDir" />
      <LockedRow label="Java path" :value="engine.javaPath" />

      <div class="engine-upgrade">
        <div v-if="!engine.upgradeAvailable" class="uptodate">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 6L9 17l-5-5" /></svg>
          Up to date
        </div>
        <div v-else class="upgrade-avail">
          <div class="upgrade-avail__text">A newer pinned version <b>{{ engine.availableVersion }}</b> is available.</div>
          <AppButton variant="primary" size="md" :loading="upgrading" @click="startUpgrade">
            Upgrade to {{ engine.availableVersion }}
          </AppButton>
        </div>

        <Stepper v-if="upgradeShown" class="stepper-block" orientation="vertical" :steps="stepperSteps" :current="currentStep" />
      </div>
    </template>
  </SurfaceCard>
</template>

<style scoped>
.mode-badge {
  padding: 4px 11px;
  border-radius: var(--radius-pill);
  background: var(--accentSoft);
  color: var(--accentBright);
  font-size: var(--text-xs);
  font-weight: var(--weight-extrabold);
}

.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--set-ok-dot);
}

.status-text {
  font-size: var(--text-sm);
  color: var(--set-ok-text);
  font-weight: var(--weight-bold);
}

.mono {
  font-family: var(--font-mono);
  font-size: 12.5px;
  color: var(--text);
}

.engine-upgrade {
  border-top: 1px solid var(--border);
  padding-top: 14px;
  margin-top: 4px;
}

.uptodate {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--text-base);
  color: var(--set-ok-text);
  font-weight: var(--weight-bold);
}

.upgrade-avail {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
}

.upgrade-avail__text {
  font-size: var(--text-base);
  color: var(--muted);
}

.upgrade-avail__text b {
  color: var(--text);
}

.stepper-block {
  margin-top: 14px;
}
</style>
