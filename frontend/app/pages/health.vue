<script setup lang="ts">
/**
 * Library Health page — route "/health".
 *
 * Delegates all data fetching and state to useHealth(), which is auto-imported
 * from app/composables/useHealth.ts. LibraryHealth is auto-imported from
 * app/components/. navigateTo is a Nuxt auto-import.
 *
 * Prop wiring:
 *   :series      — SeriesHealth[] (unhealthy series from GET /api/health)
 *   :loading     — true during the initial load (skeleton cards)
 *   :refreshing  — true during a manual re-poll (Rescan spinner)
 *
 * Emit wiring:
 *   @open-series → navigateTo('/series/' + id)
 *   @refresh     → refresh() (re-polls GET /api/health, sets refreshing)
 */
const { series, pending, refreshing, refresh } = useHealth()
</script>

<template>
  <div class="page-health">
    <LibraryHealth
      :series="series"
      :loading="pending"
      :refreshing="refreshing"
      @open-series="(id: string) => navigateTo(`/series/${id}`)"
      @refresh="refresh"
    />
  </div>
</template>

<style scoped>
.page-health {
  min-height: 100%;
}
</style>
