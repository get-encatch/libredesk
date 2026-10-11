<script setup>
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, ArrowRight, Plus, RotateCw, Search } from '@lucide/vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { filtersOf, listQuery, STATUS_WAITING } from '@/filters'
import { initials, longDate, mailDate } from '@/format'
import { state } from '@/session'
import StatusPill from './StatusPill.vue'

// The ticket list pane, shared by the list page (wide: one-line rows from lg) and the
// ticket page (a narrow column next to the open ticket, from lg only).
const props = defineProps({
  list: { type: Object, default: null },
  loading: { type: Boolean, default: false },
  error: { type: String, default: '' },
  activeRef: { type: String, default: '' },
  wide: { type: Boolean, default: false },
})
const emit = defineEmits(['retry'])

const route = useRoute()
const router = useRouter()
const filters = computed(() => filtersOf(route.query))
const tickets = computed(() => props.list?.tickets ?? [])
const paged = computed(() => props.list && (props.list.has_newer || props.list.has_older))

const search = ref(filters.value.q)
watch(() => filters.value.q, (q) => { search.value = q })

function submitSearch() {
  const { status, project } = filters.value
  router.push({ name: 'list', query: listQuery({ status, project, q: search.value.trim() }) })
}

const heading = computed(() => props.list?.status || filters.value.status || 'All tickets')
const projectName = computed(() => props.list?.project_name || '')
const count = computed(() => {
  if (!props.list) return ''
  if (paged.value) return `Page ${props.list.page}`
  return `${tickets.value.length} ${tickets.value.length === 1 ? 'ticket' : 'tickets'}`
})

// Column templates: Project and Raised by show from xl, when there are any.
const cols = computed(() => {
  const project = props.list?.show_project
  const raised = props.list?.show_raised_by
  const xl = project && raised
    ? 'xl:grid-cols-[13rem_minmax(0,1fr)_9rem_10rem_8.5rem_4.5rem]'
    : project ? 'xl:grid-cols-[13rem_minmax(0,1fr)_9rem_8.5rem_4.5rem]'
      : raised ? 'xl:grid-cols-[13rem_minmax(0,1fr)_10rem_8.5rem_4.5rem]' : ''
  return `lg:grid-cols-[13rem_minmax(0,1fr)_8.5rem_4.5rem] ${xl}`
})

// Ticket links keep the list's filters and page.
const ticketLink = (ref) => ({ name: 'ticket', params: { ref }, query: listQuery(filters.value) })
const pageLink = (page) => ({ name: 'list', query: listQuery({ ...filters.value, page }) })

const empty = computed(() => {
  const l = props.list
  const filtered = !!(l.status || l.project_name)
  if (l.has_newer) return { title: 'No older tickets', text: "You've reached the end of this list." }
  if (l.search) return { title: `No tickets match “${l.search}”`, text: 'Try another search or view.', clear: true }
  if (filtered) return { title: 'No tickets in this view', text: 'Try another search or view.' }
  return { title: 'No tickets yet', text: "Raise a ticket and we'll get back to you.", create: state.session.can_create }
})
const clearSearch = computed(() => ({ name: 'list', query: listQuery({ status: filters.value.status, project: filters.value.project }) }))
</script>

<template>
  <section
    class="flex-col"
    :class="wide ? 'flex lg:flex-1' : 'hidden lg:flex lg:w-80 lg:shrink-0 lg:border-r xl:w-96'"
    aria-label="Tickets"
  >
    <div class="space-y-3 border-b px-4 py-3" :class="wide && 'lg:flex lg:items-center lg:justify-between lg:gap-6 lg:space-y-0 lg:px-6 lg:py-4'">
      <div class="flex items-baseline justify-between gap-2">
        <h1 class="truncate text-base font-semibold tracking-tight" :class="wide && 'lg:text-xl'">
          {{ heading }}<span v-if="projectName" class="font-normal text-muted-foreground"> · {{ projectName }}</span>
        </h1>
        <span class="shrink-0 text-xs text-muted-foreground">{{ count }}</span>
      </div>
      <form class="relative" :class="wide && 'lg:w-80 lg:shrink-0'" role="search" @submit.prevent="submitSearch">
          <Search class="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            v-model="search"
            type="search"
            name="q"
            :maxlength="state.session.limits.search"
            placeholder="Search by subject or #number"
            aria-label="Search tickets"
            class="pl-8"
          />
      </form>
    </div>

    <Alert v-if="error" variant="destructive" class="m-4 w-auto">
      <AlertDescription class="flex items-center justify-between gap-3">
        <span>{{ error }}</span>
        <Button variant="outline" size="sm" @click="emit('retry')"><RotateCw />Try again</Button>
      </AlertDescription>
    </Alert>

    <div
      v-if="wide && tickets.length"
      class="hidden gap-4 border-b px-6 py-2 text-xs font-medium text-muted-foreground lg:grid"
      :class="cols"
      aria-hidden="true"
    >
      <span>From</span><span>Subject</span>
      <span v-if="list.show_project" class="hidden xl:block">Project</span>
      <span v-if="list.show_raised_by" class="hidden xl:block">Raised by</span>
      <span>Status</span><span class="text-right">Updated</span>
    </div>

    <ul class="divide-y lg:flex-1 lg:overflow-y-auto" :class="loading && list && 'opacity-60 transition-opacity'" :aria-busy="loading">
      <template v-if="!list && !error">
        <li v-for="n in 6" :key="n" class="flex flex-col gap-2 px-4 py-3" :class="wide && 'lg:px-6'">
          <Skeleton class="h-4 w-2/3" />
          <Skeleton class="h-3 w-1/3" />
        </li>
      </template>
      <template v-else-if="list">
        <li v-for="t in tickets" :key="t.ref">
          <RouterLink
            :to="ticketLink(t.ref)"
            class="flex flex-col gap-1.5 px-4 py-3 transition-colors hover:bg-accent"
            :class="[t.ref === activeRef && 'bg-accent', wide && 'lg:hidden']"
            :aria-current="t.ref === activeRef ? 'page' : undefined"
          >
            <div class="flex items-center gap-2">
              <span v-if="t.status === STATUS_WAITING" class="size-2 shrink-0 rounded-full bg-primary" title="Waiting on your reply" />
              <span class="truncate text-sm" :class="t.status === STATUS_WAITING ? 'font-semibold' : 'font-medium'">{{ t.subject }}</span>
              <time class="ml-auto shrink-0 text-xs text-muted-foreground" :datetime="t.updated_at" :title="longDate(t.updated_at)">{{ mailDate(t.updated_at) }}</time>
            </div>
            <p v-if="t.preview" class="line-clamp-2 text-xs leading-relaxed text-muted-foreground [overflow-wrap:anywhere]">{{ t.preview }}</p>
            <div class="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Avatar class="size-5" aria-hidden="true">
                <AvatarFallback class="text-[10px] font-semibold" :class="t.from_support ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'">{{ initials(t.sender) }}</AvatarFallback>
              </Avatar>
              <span class="min-w-0 truncate">{{ t.sender }} · #{{ t.ref }}<template v-if="list.show_project && t.project_name"> · {{ t.project_name }}</template></span>
              <span class="ml-auto shrink-0"><StatusPill :status="t.status" /></span>
            </div>
          </RouterLink>
          <RouterLink
            v-if="wide"
            :to="ticketLink(t.ref)"
            class="hidden items-center gap-4 px-6 py-2.5 text-sm transition-colors hover:bg-accent lg:grid"
            :class="[cols, t.status === STATUS_WAITING && 'font-semibold']"
          >
            <span class="flex min-w-0 items-center gap-2">
              <span class="size-2 shrink-0 rounded-full bg-primary" :class="t.status !== STATUS_WAITING && 'invisible'" />
              <Avatar class="size-5" aria-hidden="true">
                <AvatarFallback class="text-[10px] font-semibold" :class="t.from_support ? 'bg-primary text-primary-foreground' : 'bg-muted text-muted-foreground'">{{ initials(t.sender) }}</AvatarFallback>
              </Avatar>
              <span class="truncate">{{ t.sender }}</span>
            </span>
            <span class="min-w-0 truncate">
              <span class="text-muted-foreground">#{{ t.ref }}</span>
              {{ ' ' }}<span :class="t.status === STATUS_WAITING ? 'font-semibold' : 'font-medium'">{{ t.subject }}</span>
              <span v-if="t.preview" class="text-muted-foreground"> — {{ t.preview }}</span>
            </span>
            <span v-if="list.show_project" class="hidden min-w-0 truncate text-muted-foreground xl:block">{{ t.project_name || '—' }}</span>
            <span v-if="list.show_raised_by" class="hidden min-w-0 truncate text-muted-foreground xl:block">{{ t.raised_by }}</span>
            <span><StatusPill :status="t.status" /></span>
            <time class="text-right text-xs text-muted-foreground" :datetime="t.updated_at" :title="longDate(t.updated_at)">{{ mailDate(t.updated_at) }}</time>
          </RouterLink>
        </li>
        <li v-if="!tickets.length" class="px-4 py-12 text-center">
          <p class="text-sm font-medium">{{ empty.title }}</p>
          <p class="mt-1 text-xs text-muted-foreground">{{ empty.text }}</p>
          <Button v-if="empty.clear" as-child variant="outline" size="sm" class="mt-4">
            <RouterLink :to="clearSearch">Clear search</RouterLink>
          </Button>
          <Button v-else-if="empty.create" as-child size="sm" class="mt-4">
            <RouterLink :to="{ name: 'new' }"><Plus />New ticket</RouterLink>
          </Button>
        </li>
      </template>
    </ul>

    <nav v-if="paged" class="flex items-center justify-between gap-2 border-t px-4 py-2" aria-label="Pages">
      <Button v-if="list.has_newer" as-child variant="ghost" size="sm">
        <RouterLink :to="pageLink(list.page - 1)" rel="prev"><ArrowLeft />Newer</RouterLink>
      </Button>
      <span v-else />
      <span class="text-xs text-muted-foreground">Page {{ list.page }}</span>
      <Button v-if="list.has_older" as-child variant="ghost" size="sm">
        <RouterLink :to="pageLink(list.page + 1)" rel="next">Older<ArrowRight /></RouterLink>
      </Button>
      <span v-else />
    </nav>
  </section>
</template>
