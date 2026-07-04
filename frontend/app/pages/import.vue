<script setup lang="ts">
/**
 * Import page — route "/import".
 *
 * Delegates all data fetching and wizard state to useImport() (auto-imported).
 * Import is auto-imported from app/components/screens/. navigateTo is a Nuxt
 * auto-import.
 *
 * Discover hand-off: if opened from Discover (/import?source=&mangaId=&title=),
 * useImport pre-seeds an inspect call so the chapter list is ready for Stage 2.
 *
 * Emit wiring:
 *   @search         → search({ q, sources })
 *   @inspect        → inspect({ source, mangaId })
 *   @load-breakdowns → loadBreakdowns(candidates) — Stage 2 entry, per-scanlator
 *                      auto-split coverage (fetched in parallel, cached, per-
 *                      source failures non-fatal — see useImport's doc comment).
 *   @adopt          → onAdopt: call adopt(req) then navigateTo /series/{id} on success
 *   @cancel         → navigateTo('/')
 *   @step           → screen-internal step tracking; no parent action needed (no-op)
 */
import type { AdoptRequest } from '~/components/screens/import.types'

const {
  sources,
  categories,
  searchResults,
  searching,
  searched,
  inspectChapters,
  adopting,
  error,
  newSeriesId,
  breakdowns,
  search,
  inspect,
  loadBreakdowns,
  adopt,
} = useImport()

async function onAdopt(req: AdoptRequest): Promise<void> {
  await adopt(req)
  // On success, newSeriesId is set; navigate to the new series detail page.
  // If adopt failed, error is set and displayed by the Import component instead.
  if (newSeriesId.value) {
    await navigateTo(`/series/${newSeriesId.value}`)
  }
}
</script>

<template>
  <div class="page-import">
    <Import
      :sources="sources"
      :search-results="searchResults"
      :searching="searching"
      :searched="searched"
      :inspect-chapters="inspectChapters"
      :adopting="adopting"
      :error="error"
      :categories="categories"
      :breakdowns="breakdowns"
      @search="search"
      @inspect="inspect"
      @load-breakdowns="loadBreakdowns"
      @adopt="onAdopt"
      @cancel="navigateTo('/')"
      @step="() => {}"
    />
  </div>
</template>

<style scoped>
.page-import {
  min-height: 100%;
}
</style>
