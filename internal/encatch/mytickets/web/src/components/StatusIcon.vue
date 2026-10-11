<script setup>
import { CircleCheck, CircleDot, Clock, Inbox } from '@lucide/vue'
import { STATUS_OPEN, STATUS_RESOLVED, STATUS_WAITING } from '@/filters'

// The sidebar's icon for a status filter ("" = all tickets), in the status colour.
defineProps({ status: { type: String, default: '' } })
</script>

<template>
  <CircleDot v-if="status === STATUS_OPEN" class="text-badge-primary-text" />
  <Clock v-else-if="status === STATUS_WAITING" class="text-warning-foreground" />
  <CircleCheck v-else-if="status === STATUS_RESOLVED" class="text-success" />
  <Inbox v-else />
</template>
