<template>
  <!-- Tsundoku SPA root. NuxtLayout selects layouts/default.vue (shell chrome)
       or layouts/bare.vue (login, no nav) per the page's definePageMeta layout.
       Pages that don't declare a layout get default (AppShell) automatically. -->
  <div id="app">
    <NuxtLayout>
      <NuxtPage />
    </NuxtLayout>
    <!-- App-global in-app new-chapter toast — active under every layout (incl.
         the reader's bare layout), unlike a layout-scoped handler. -->
    <ChapterNotifier />
  </div>
</template>
