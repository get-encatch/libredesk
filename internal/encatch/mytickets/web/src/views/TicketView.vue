<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Lock, MessageSquare, X } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import InboxList from '@/components/InboxList.vue'
import ReplyBox from '@/components/ReplyBox.vue'
import StatusPill from '@/components/StatusPill.vue'
import TicketDetails from '@/components/TicketDetails.vue'
import TicketThread from '@/components/TicketThread.vue'
import { filtersOf, listQuery } from '@/filters'
import { useTicket } from '@/ticket'
import { useTickets } from '@/tickets'
import { useTitle } from '@/title'

// A ticket next to the list (from lg), with its details (from xl). Below lg only the
// ticket shows, with a back button to the list.
const route = useRoute()
const router = useRouter()
const ref_ = computed(() => String(route.params.ref))
const { list, loading: listLoading, error: listError, reload: reloadList } = useTickets()
const { view, error, reload } = useTicket()

// The view for this URL's ticket; while another loads, the old one isn't shown.
const current = computed(() => (view.value?.ticket.ref === ref_.value ? view.value : null))
const backToList = computed(() => ({ name: 'list', query: listQuery(filtersOf(route.query)) }))
useTitle(() => current.value?.ticket.subject || `#${ref_.value}`)

const body = ref(null)

// After a reply: show the newest messages (with the reply at the bottom), refresh
// the list's previews and scroll to the reply.
async function onSent() {
  if (route.query.m) {
    const { m, ...rest } = route.query
    await router.replace({ query: rest })
  }
  // Load again even if the URL change already started a load: the newest load wins,
  // and waiting for it lets the scroll below land on the reply.
  reloadList()
  await reload()
  await nextTick()
  body.value?.scrollTo({ top: body.value.scrollHeight, behavior: 'smooth' })
  if (window.innerWidth < 1024) document.querySelector('#message')?.scrollIntoView({ block: 'end', behavior: 'smooth' })
}

// A new page of the thread starts at the top of the pane.
watch(() => [route.params.ref, route.query.m], () => body.value?.scrollTo({ top: 0 }))
</script>

<template>
  <div class="lg:flex lg:h-svh lg:overflow-hidden">
    <InboxList :list="list" :loading="listLoading" :error="listError" :active-ref="ref_" @retry="reloadList" />

    <section class="flex min-w-0 flex-col lg:flex-1" aria-label="Conversation">
      <header class="flex h-14 shrink-0 items-center gap-2 border-b px-4 lg:px-6">
        <Button as-child variant="ghost" size="icon-sm" class="-ml-2 text-muted-foreground hover:text-foreground lg:hidden" aria-label="Back to tickets">
          <RouterLink :to="backToList"><ArrowLeft /></RouterLink>
        </Button>
        <MessageSquare class="hidden size-4 shrink-0 text-muted-foreground lg:block" />
        <h2 class="min-w-0 flex-1 truncate text-sm font-semibold" :title="current?.ticket.subject">
          {{ current?.ticket.subject }}
        </h2>
        <StatusPill v-if="current" :status="current.ticket.status" />
        <Button
          as-child
          variant="ghost"
          size="icon-sm"
          class="-mr-2 hidden text-muted-foreground hover:text-foreground lg:inline-flex"
          aria-label="Back to the list"
          title="Back to the list"
        >
          <RouterLink :to="backToList"><X /></RouterLink>
        </Button>
      </header>

      <div ref="body" class="px-4 py-6 lg:flex-1 lg:overflow-y-auto lg:px-6">
        <div v-if="error" class="py-16 text-center">
          <p class="text-sm font-medium">{{ error.message }}</p>
          <Button as-child variant="outline" size="sm" class="mt-4">
            <RouterLink :to="backToList">Back to tickets</RouterLink>
          </Button>
        </div>
        <TicketThread v-else-if="current" :view="current" />
        <div v-else class="space-y-8" aria-busy="true">
          <div v-for="n in 3" :key="n" class="flex gap-3">
            <Skeleton class="size-8 rounded-lg" />
            <div class="flex-1 space-y-2">
              <Skeleton class="h-4 w-40" />
              <Skeleton class="h-3 w-24" />
              <Skeleton class="h-12 w-full" />
            </div>
          </div>
        </div>
      </div>

      <template v-if="current">
        <ReplyBox
          v-if="current.can_reply"
          :key="current.ticket.ref"
          :ticket-ref="current.ticket.ref"
          :status="current.ticket.status"
          @sent="onSent"
        />
        <div v-else class="border-t px-4 py-3 lg:px-6">
          <p class="flex items-center gap-2 rounded-md border border-dashed px-3 py-2.5 text-sm text-muted-foreground">
            <Lock class="size-4 shrink-0" />
            <span>You have read-only access to this ticket. Ask your organisation admin for Manage access to reply.</span>
          </p>
        </div>
      </template>
    </section>

    <TicketDetails v-if="current" :ticket="current.ticket" />
  </div>
</template>
