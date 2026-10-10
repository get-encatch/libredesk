<!--
  encatch: admin page listing every "Remove for security" action (who, when,
  what, why), from our encatch_redactions table. Route: /admin/teams/removal-log,
  shown to whoever can see libredesk's activity log (admins by default).
-->
<template>
  <div class="mx-auto w-full max-w-7xl min-h-screen p-0 sm:px-6 lg:px-10">
    <div class="flex flex-col gap-4 pb-8">
      <div class="space-y-1">
        <h1 class="text-xl font-semibold">Removal log</h1>
        <p class="text-sm text-muted-foreground">
          Text and files removed from conversations for security, with the reason given. Removed content can’t be restored.
        </p>
      </div>

      <form class="flex items-center gap-2" @submit.prevent="search">
        <div class="relative w-full max-w-sm">
          <Search class="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input v-model="query" class="h-8 pl-8" placeholder="Search reason, file, agent or #ticket" />
        </div>
        <Button type="submit" variant="outline" size="sm" class="h-8">Search</Button>
        <Button v-if="applied" type="button" variant="ghost" size="sm" class="h-8" @click="clear">Clear</Button>
      </form>

      <div class="w-full overflow-x-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead class="whitespace-nowrap">When</TableHead>
              <TableHead>Ticket</TableHead>
              <TableHead>Removed</TableHead>
              <TableHead>From</TableHead>
              <TableHead>By</TableHead>
              <TableHead class="min-w-[16rem]">Reason</TableHead>
              <TableHead>Exposure</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-if="loading">
              <TableCell colspan="7" class="py-10 text-center text-muted-foreground">Loading…</TableCell>
            </TableRow>
            <TableRow v-else-if="!rows.length">
              <TableCell colspan="7" class="py-10 text-center text-muted-foreground">
                {{ applied ? 'No removals match your search.' : 'Nothing has been removed yet.' }}
              </TableCell>
            </TableRow>
            <TableRow v-for="r in rows" v-else :key="r.id">
              <TableCell class="whitespace-nowrap text-sm" :title="full(r.created_at)">{{ short(r.created_at) }}</TableCell>
              <TableCell>
                <router-link :to="`/inboxes/all/conversation/${r.conversation_uuid}`" class="text-sm font-medium hover:underline">
                  #{{ r.conversation_reference || '—' }}
                </router-link>
              </TableCell>
              <TableCell>
                <span v-if="r.kind === 'text'" class="inline-flex items-center gap-1.5 text-sm">
                  <TextCursorInput class="h-3.5 w-3.5 text-muted-foreground" />Message text
                </span>
                <span v-else class="inline-flex items-center gap-1.5 text-sm" :title="r.file_name">
                  <Paperclip class="h-3.5 w-3.5 shrink-0 text-muted-foreground" /><span class="max-w-[12rem] truncate">{{ r.file_name }}</span>
                </span>
                <Badge v-if="r.outcome !== 'done'" variant="destructive" class="ml-2" :title="r.outcome">
                  {{ r.outcome === 'pending' ? 'Incomplete' : 'Failed' }}
                </Badge>
              </TableCell>
              <TableCell class="whitespace-nowrap text-sm">{{ source(r) }}</TableCell>
              <TableCell class="whitespace-nowrap text-sm">
                {{ r.actor_name }}
                <Badge v-if="r.actor_is_admin" variant="outline" class="ml-1">Admin</Badge>
              </TableCell>
              <TableCell class="text-sm">{{ r.reason }}</TableCell>
              <TableCell>
                <Badge v-if="r.exposure === 'emailed'" variant="outline" class="whitespace-nowrap border-amber-500/50 text-amber-700 dark:text-amber-400">Emailed to customer</Badge>
                <Badge v-else-if="r.exposure === 'from_customer'" variant="outline" class="whitespace-nowrap">Sent by customer</Badge>
                <span v-else class="text-sm text-muted-foreground">Helpdesk only</span>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>

      <PaginationBar v-model:page="page" v-model:per-page="perPage" :total-pages="totalPages" />
    </div>
  </div>
</template>

<script setup>
import { onMounted, ref, watch } from 'vue'
import { format } from 'date-fns'
import { Search, Paperclip, TextCursorInput } from 'lucide-vue-next'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@shared-ui/components/ui/table'
import { Badge } from '@shared-ui/components/ui/badge'
import { Button } from '@shared-ui/components/ui/button'
import { Input } from '@shared-ui/components/ui/input'
import PaginationBar from '@main/components/pagination/PaginationBar.vue'
import { useEmitter } from '@main/composables/useEmitter'
import { EMITTER_EVENTS } from '@main/constants/emitterEvents'
import { handleHTTPError } from '@shared-ui/utils/http.js'
import { listRemovals } from './api'

const emitter = useEmitter()
const rows = ref([])
const loading = ref(true)
const page = ref(1)
const perPage = ref(15) // one of PaginationBar's options (15, 30, 50, 100)
const totalPages = ref(0)
const query = ref('')
const applied = ref('')

const short = (ts) => format(new Date(ts), 'd MMM yyyy, HH:mm')
const full = (ts) => format(new Date(ts), 'EEEE d MMMM yyyy, HH:mm:ss')
const source = (r) => {
  if (r.message_type === 'incoming') return 'Customer message'
  return r.message_private ? 'Private note' : 'Agent reply'
}

const load = async () => {
  loading.value = true
  try {
    const resp = await listRemovals({ page: page.value, per_page: perPage.value, search: applied.value || undefined })
    const d = resp.data.data
    rows.value = d.results || []
    totalPages.value = Math.max(1, d.total_pages || 0) // "Page 1 of 1" when empty, not "of 0"
  } catch (error) {
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, { variant: 'destructive', description: handleHTTPError(error).message })
  } finally {
    loading.value = false
  }
}

const search = () => {
  applied.value = query.value.trim()
  if (page.value !== 1) page.value = 1
  else load()
}
const clear = () => {
  query.value = ''
  search()
}

watch([page, perPage], load)
onMounted(load)
</script>
