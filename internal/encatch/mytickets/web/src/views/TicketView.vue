<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import InboxList from '@/components/InboxList.vue'
import { useTickets } from '@/tickets'
import { useTitle } from '@/title'

// The thread, reply box and details panel arrive in the next pass (V3); for now the
// list column and the ticket's number.
const route = useRoute()
const ref = computed(() => String(route.params.ref))
const { list, loading, error, reload } = useTickets()
useTitle(() => `#${ref.value}`)
</script>

<template>
  <div class="lg:flex lg:h-svh lg:overflow-hidden">
    <InboxList :list="list" :loading="loading" :error="error" :active-ref="ref" @retry="reload" />
    <section class="flex min-w-0 flex-col items-center justify-center gap-1 px-4 py-16 text-center lg:flex-1" aria-label="Conversation">
      <p class="text-sm font-medium">Ticket #{{ ref }}</p>
      <p class="text-xs text-muted-foreground">The conversation view is coming in the next pass.</p>
    </section>
  </div>
</template>
