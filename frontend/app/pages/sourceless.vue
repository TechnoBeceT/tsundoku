<script setup lang="ts">
/**
 * Sourceless — LEGACY ROUTE, kept as a REDIRECT to "/cleanup?tab=sourceless".
 *
 * The library-wide Sourceless surface moved into the 3-tab Cleanup console; the
 * screen component itself is unchanged and is now rendered as that console's
 * second tab. This file exists solely so bookmarks and any in-app link to the old
 * path keep working — the Cleanup page's `?tab=` deep-link resolution beats its
 * stored session tab, so the redirect always lands on Sourceless.
 *
 * vue-router applies the redirect before this component renders, so the template
 * below is never shown.
 */
definePageMeta({
  redirect: { path: '/cleanup', query: { tab: 'sourceless' } },
})
</script>

<template>
  <div />
</template>
