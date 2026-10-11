<script setup>
import { Info } from '@lucide/vue'
import { longDate } from '@/format'
import { state } from '@/session'
import StatusBadge from './StatusBadge.vue'

defineProps({ ticket: { type: Object, required: true } })
</script>

<template>
  <aside class="hidden w-72 shrink-0 overflow-y-auto border-l px-5 py-5 xl:block" aria-label="Ticket details">
    <h2 class="flex items-center gap-2 text-sm font-semibold"><Info class="size-4" />Details</h2>
    <dl class="mt-4 space-y-4 text-sm">
      <div>
        <dt class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Status</dt>
        <dd class="mt-1"><StatusBadge :status="ticket.status" /></dd>
      </div>
      <div>
        <dt class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Ticket</dt>
        <dd class="mt-1">#{{ ticket.ref }}</dd>
      </div>
      <div v-if="ticket.project_name">
        <dt class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Project</dt>
        <dd class="mt-1">{{ ticket.project_name }}</dd>
      </div>
      <div>
        <dt class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Raised by</dt>
        <dd class="mt-1">{{ ticket.raised_by_you ? 'You' : ticket.raised_by }}</dd>
      </div>
      <div>
        <dt class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Opened</dt>
        <dd class="mt-1">{{ longDate(ticket.created_at) }}</dd>
      </div>
      <div>
        <dt class="text-xs font-medium uppercase tracking-wide text-muted-foreground">Organisation</dt>
        <dd class="mt-1">{{ state.session.org.name }}</dd>
      </div>
    </dl>
    <p class="mt-8 border-t pt-4 text-xs leading-relaxed text-muted-foreground">
      Replies here reach the Encatch support team. We also email you at {{ state.session.user.email }} when we reply.
    </p>
  </aside>
</template>
