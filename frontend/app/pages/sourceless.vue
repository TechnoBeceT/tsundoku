<script setup lang="ts">
/**
 * Sourceless page — route "/sourceless".
 *
 * The library-wide "clean up sourceless chapters in one place" surface. Data +
 * actions come from useSourceless(); the Sourceless screen and the reused
 * SourcelessCleanupDialog are auto-imported from app/components/.
 *
 * The cleanup dialog lives HERE (not in the screen) for the same reason it
 * lives on the Fractionals page: only the page learns whether a removal
 * succeeded, so it closes the dialog ONLY on success and shows the failure
 * inside it otherwise (§16). Clicking "Review" fetches that series' removable
 * preview, then opens the dialog.
 */
import { computed, ref } from 'vue'
import type { SourcelessCleanupPreview } from '~/components/screens/sourceless.types'

const {
  series,
  pending,
  refreshing,
  error,
  removeBusy,
  removeError,
  refresh,
  fetchPreview,
  removeSourceless,
} = useSourceless()

// ---- Cleanup dialog (owned by the page, per §16) ---------------------------
const cleanupOpen = ref(false)
const cleanupSeriesId = ref<string | null>(null)
const cleanupPreview = ref<SourcelessCleanupPreview | null>(null)

const cleanupSeriesTitle = computed(() =>
  series.value.find((s) => s.seriesId === cleanupSeriesId.value)?.displayName ?? '',
)

async function openCleanup(seriesId: string): Promise<void> {
  cleanupSeriesId.value = seriesId
  cleanupPreview.value = await fetchPreview(seriesId)
  cleanupOpen.value = true
}

async function onConfirmCleanup(chapterIds: string[]): Promise<void> {
  if (!cleanupSeriesId.value) return
  const ok = await removeSourceless(cleanupSeriesId.value, chapterIds)
  if (ok) cleanupOpen.value = false
}
</script>

<template>
  <div class="page-sourceless">
    <ErrorBanner v-if="error" :message="error" />
    <Sourceless
      :series="series"
      :loading="pending"
      :refreshing="refreshing"
      @review="openCleanup"
      @refresh="refresh"
    />

    <SourcelessCleanupDialog
      :open="cleanupOpen"
      :series-title="cleanupSeriesTitle"
      :preview="cleanupPreview"
      :busy="removeBusy"
      :error="removeError"
      @confirm="onConfirmCleanup"
      @close="cleanupOpen = false"
    />
  </div>
</template>

<style scoped>
.page-sourceless {
  min-height: 100%;
}
</style>
