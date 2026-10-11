<script setup>
import logoLight from '../../../static/logo-light.svg'
import logoDark from '../../../static/logo-dark.png'
</script>

<template>
  <RouterLink :to="{ name: 'list' }" class="flex items-center gap-2.5 text-foreground">
    <img :src="logoLight" alt="Encatch" class="h-6 w-auto dark:hidden" width="125" height="24">
    <img :src="logoDark" alt="Encatch" class="hidden h-6 w-auto dark:block" width="125" height="24">
    <span class="border-l pl-2.5 text-sm font-medium text-muted-foreground">Support</span>
  </RouterLink>
</template>
