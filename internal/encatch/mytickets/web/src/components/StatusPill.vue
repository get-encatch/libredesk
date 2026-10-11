<script setup>
import { STATUS_RESOLVED, STATUS_WAITING } from '@/filters'

defineProps({ status: { type: String, required: true } })
</script>

<template>
  <span class="inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border px-2 py-0.5 text-xs font-medium">
    <span
      class="size-1.5 rounded-full"
      :class="status === STATUS_WAITING ? 'bg-warning-foreground' : status === STATUS_RESOLVED ? 'bg-success' : 'bg-badge-primary-text'"
    />
    {{ status }}
  </span>
</template>
