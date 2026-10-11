<script setup>
import { useTitle } from '@/title'

// The new-ticket form arrives in pass V4.
useTitle(() => 'New ticket')
</script>

<template>
  <div class="mx-auto max-w-3xl px-4 pb-16 pt-6 md:px-8 md:pt-10">
    <h1 class="text-2xl font-semibold tracking-tight">New ticket</h1>
    <p class="mt-1 text-sm text-muted-foreground">The new-ticket form is coming in a later pass.</p>
  </div>
</template>
