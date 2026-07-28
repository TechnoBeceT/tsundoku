<script setup lang="ts">
/**
 * Fractionals — LEGACY ROUTE, kept as a REDIRECT to "/cleanup?tab=fractionals".
 *
 * The library-wide Fractionals surface moved into the 3-tab Cleanup console; the
 * screen component itself is unchanged and is now rendered as that console's first
 * tab. This file exists solely so bookmarks and any in-app link to the old path
 * keep working — the Cleanup page's `?tab=` deep-link resolution beats its stored
 * session tab, so the redirect always lands on Fractionals.
 *
 * vue-router applies the redirect before this component renders, so the template
 * below is never shown.
 */
definePageMeta({
  redirect: { path: '/cleanup', query: { tab: 'fractionals' } },
})
</script>

<template>
  <div />
</template>
