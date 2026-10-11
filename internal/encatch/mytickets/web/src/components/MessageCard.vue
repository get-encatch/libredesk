<script setup>
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import Brand from './Brand.vue'
import { useTitle } from '@/title'

// A full-page message when there is no session to show: signed out, session ended,
// or the app couldn't start.
const props = defineProps({
  title: { type: String, required: true },
  text: { type: String, default: '' },
})
useTitle(() => props.title)
</script>

<template>
  <div class="min-h-screen">
    <header class="border-b">
      <div class="mx-auto flex max-w-3xl items-center px-4 py-3">
        <Brand />
      </div>
    </header>
    <main class="mx-auto max-w-3xl px-4 pb-16 pt-8">
      <Card class="mx-auto mt-10 max-w-md">
        <CardHeader>
          <CardTitle class="text-xl">{{ title }}</CardTitle>
          <CardDescription class="leading-relaxed">{{ text }}</CardDescription>
        </CardHeader>
      </Card>
    </main>
  </div>
</template>
