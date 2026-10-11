<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { Paperclip } from '@lucide/vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { filtersOf, listQuery, STATUS_WAITING } from '@/filters'
import { longDate } from '@/format'

// A ticket's messages (one page of them, oldest first), with the paging bar and
// the subject block shown when the details panel is hidden.
const props = defineProps({ view: { type: Object, required: true } })

const route = useRoute()
const t = computed(() => props.view.ticket)
const th = computed(() => props.view.thread)
const paged = computed(() => th.value.total > th.value.per_page)

// Thread pages keep the list's filters.
const pageLink = (m) => ({
  name: 'ticket',
  params: { ref: t.value.ref },
  query: { ...listQuery(filtersOf(route.query)), ...(m > 1 ? { m: String(m) } : {}) },
})
</script>

<template>
  <div>
    <div class="mb-6 xl:hidden">
      <p class="text-lg font-semibold leading-snug tracking-tight [overflow-wrap:anywhere]">{{ t.subject }}</p>
      <p class="mt-1 text-xs text-muted-foreground">
        #{{ t.ref }} · opened {{ longDate(t.created_at) }}<template v-if="t.project_name"> · {{ t.project_name }}</template><template v-if="!t.raised_by_you"> · raised by {{ t.raised_by }}</template>
      </p>
    </div>

    <Alert v-if="t.status === STATUS_WAITING" class="mb-6 border-warning-foreground bg-warning">
      <AlertDescription class="text-foreground">We're waiting for your reply. Add any details below.</AlertDescription>
    </Alert>

    <div v-if="paged" class="mb-6 flex flex-wrap items-center justify-between gap-2 border-b pb-3">
      <Button v-if="th.has_earlier" as-child variant="outline" size="sm">
        <RouterLink :to="pageLink(th.page + 1)">Show earlier messages</RouterLink>
      </Button>
      <span v-else class="text-xs text-muted-foreground">Start of the conversation</span>
      <span class="text-xs text-muted-foreground">Messages {{ th.from }}–{{ th.to }} of {{ th.total }}</span>
    </div>

    <div class="space-y-8">
      <article v-for="(m, i) in view.messages" :key="`${th.page}-${i}`" class="flex gap-3">
        <Avatar class="size-8 rounded-lg" aria-hidden="true">
          <AvatarFallback
            class="rounded-lg text-xs font-semibold"
            :class="m.from_customer ? 'bg-muted text-muted-foreground' : 'bg-primary text-primary-foreground'"
          >{{ m.initials }}</AvatarFallback>
        </Avatar>
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span class="text-sm font-semibold">{{ m.author }}</span>
            <Badge v-if="!m.from_customer" variant="outline" class="rounded-md font-normal text-muted-foreground">Encatch Support</Badge>
          </div>
          <p class="text-xs text-muted-foreground">{{ longDate(m.created_at) }}</p>
          <!-- Sanitised by the server (bluemonday UGC policy). -->
          <div class="message-body mt-2" v-html="m.html" />
          <ul v-if="m.attachments.length" class="mt-3 flex flex-wrap gap-2" aria-label="Attachments">
            <li v-for="a in m.attachments" :key="a.url">
              <a
                class="inline-flex max-w-64 items-center gap-1.5 rounded-md border px-2.5 py-1 text-xs text-foreground transition-colors hover:bg-accent"
                :href="a.url"
                rel="noopener"
                download
              ><Paperclip class="size-3.5 shrink-0 text-muted-foreground" /><span class="truncate">{{ a.name }}</span></a>
            </li>
          </ul>
        </div>
      </article>
      <p v-if="!view.messages.length" class="text-sm text-muted-foreground">No messages yet.</p>
    </div>

    <div v-if="th.has_newer" class="mt-8 flex justify-center border-t pt-4">
      <Button as-child variant="outline" size="sm">
        <RouterLink :to="pageLink(th.page - 1)">Show newer messages</RouterLink>
      </Button>
    </div>
  </div>
</template>
