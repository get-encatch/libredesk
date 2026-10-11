<script setup>
import { Badge } from '@/components/ui/badge'
import { STATUS_RESOLVED, STATUS_WAITING } from '@/filters'

defineProps({ status: { type: String, required: true } })
</script>

<template>
  <Badge
    variant="outline"
    class="rounded-md px-2.5 font-semibold"
    :class="status === STATUS_WAITING
      ? 'border-warning-foreground bg-warning text-warning-foreground'
      : status === STATUS_RESOLVED
        ? 'border-badge-neutral-border bg-badge-neutral-bg text-badge-neutral-text'
        : 'border-badge-primary-border bg-badge-primary-bg text-badge-primary-text'"
  >
    {{ status }}
  </Badge>
</template>
