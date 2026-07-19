<script setup lang="ts">
/**
 * Fractionals page — route "/fractionals".
 *
 * The library-wide "fix fractional chapters in one place" surface. Data + actions
 * come from useFractionals(); the Fractionals screen and the reused
 * FractionalCleanupDialog are auto-imported from app/components/.
 *
 * The cleanup dialog lives HERE (not in the screen) for the same reason it lives
 * on the Series-Detail page: only the page learns whether a removal succeeded, so
 * it closes the dialog ONLY on success and shows the failure inside it otherwise
 * (§16). Opening "Clean files" fetches that series' preview, then opens the dialog.
 */
import { ref } from 'vue'
import type { FractionalCleanupPreview } from '~/components/screens/seriesDetail.types'

const {
  series,
  pending,
  refreshing,
  error,
  togglingIds,
  toggleError,
  removeBusy,
  removeError,
  refresh,
  setIgnoreForSeries,
  fetchPreview,
  removeFractionals,
} = useFractionals()

// ---- Cleanup dialog (owned by the page, per §16) ---------------------------
const cleanupOpen = ref(false)
const cleanupSeriesId = ref<string | null>(null)
const cleanupPreview = ref<FractionalCleanupPreview | null>(null)

async function openCleanup(seriesId: string): Promise<void> {
  cleanupSeriesId.value = seriesId
  cleanupPreview.value = await fetchPreview(seriesId)
  cleanupOpen.value = true
}

async function onConfirmCleanup(chapterIds: string[]): Promise<void> {
  if (!cleanupSeriesId.value) return
  const ok = await removeFractionals(cleanupSeriesId.value, chapterIds)
  if (ok) cleanupOpen.value = false
}
</script>

<template>
  <div class="page-fractionals">
    <ErrorBanner v-if="error" :message="error" />
    <ErrorBanner v-if="toggleError" :message="toggleError" />
    <Fractionals
      :series="series"
      :loading="pending"
      :refreshing="refreshing"
      :busy-ids="togglingIds"
      @open-series="(id: string) => navigateTo(`/series/${id}`)"
      @toggle-ignore="(p: { seriesId: string, ignore: boolean }) => setIgnoreForSeries(p.seriesId, p.ignore)"
      @clean-files="openCleanup"
      @refresh="refresh"
    />

    <FractionalCleanupDialog
      v-model:open="cleanupOpen"
      :chapters="cleanupPreview?.chapters ?? []"
      :typical-page-count="cleanupPreview?.typicalPageCount ?? 0"
      :busy="removeBusy"
      :error="removeError"
      @confirm="onConfirmCleanup"
    />
  </div>
</template>

<style scoped>
.page-fractionals {
  min-height: 100%;
}
</style>
