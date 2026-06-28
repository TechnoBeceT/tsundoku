<script setup lang="ts">
/**
 * Categories page — route "/categories".
 *
 * Delegates all data fetching to useCategories(). Categories is auto-imported
 * from app/components/ (pathPrefix: false). navigateTo is a Nuxt auto-import.
 *
 * Wiring:
 *   @open-category → navigate to "/" with ?category=<name> so the library
 *                    page pre-filters to the selected category on arrival.
 */
const { categories, pending, error } = useCategories()
</script>

<template>
  <div class="page-categories">
    <ErrorBanner v-if="error" :message="error" />
    <Categories
      :categories="categories"
      :loading="pending"
      @open-category="(name: string) => navigateTo({ path: '/', query: { category: name } })"
    />
  </div>
</template>

<style scoped>
.page-categories {
  min-height: 100%;
}
</style>
