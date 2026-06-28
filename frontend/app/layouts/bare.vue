<template>
  <!-- Bare layout: no nav chrome. Used by /login. Wrapping div is required
       because vue/no-multiple-template-root disallows a bare <slot> root. -->
  <div>
    <slot />
  </div>
</template>
