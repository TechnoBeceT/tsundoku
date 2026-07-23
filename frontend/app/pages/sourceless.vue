<script setup lang="ts">
/**
 * Sourceless page — route "/sourceless".
 *
 * The library-wide "clean up sourceless chapters in one place" surface.
 * Unlike `pages/fractionals.vue`, this page is a pure mount: `Sourceless.vue`
 * owns `useSourceless()` and the reused `SourcelessCleanupDialog` directly
 * (see its doc comment) — there is no page-level routing/toggle glue needed,
 * since a sourceless row has nothing to navigate to or flag, only review.
 */
</script>

<template>
  <div class="page-sourceless">
    <Sourceless />
  </div>
</template>

<style scoped>
.page-sourceless {
  min-height: 100%;
}
</style>
