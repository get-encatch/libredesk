<!--
  encatch: Customer organisations. Each Encatch org's support tier, which My Tickets
  puts on new tickets (ticket_tier) for the SLA rules. Orgs appear when their users
  first open Support; admins can also add one by hand. Route /admin/customer-organisations,
  permission sla:manage. Backend: cmd/encatch_orgs.go.
-->
<template>
  <div class="mx-auto w-full max-w-7xl min-h-screen p-0 sm:px-6 lg:px-10">
    <div class="flex flex-col gap-4 pb-8">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="max-w-2xl space-y-1">
          <h1 class="text-xl font-semibold">Customer organisations</h1>
          <p class="text-sm text-muted-foreground">
            Support tier per Encatch organisation. New tickets from My Tickets get the org's tier, which decides the SLA.
            Orgs without a tier get {{ meta.default_tier }}. Changes apply to new tickets only.
          </p>
        </div>
        <Button size="sm" @click="openAdd"><Plus class="mr-1 h-4 w-4" />Add organisation</Button>
      </div>

      <form class="flex flex-wrap items-center gap-2" @submit.prevent="search">
        <Select v-model="instance" @update:model-value="search">
          <SelectTrigger class="h-8 w-40"><SelectValue placeholder="All instances" /></SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All instances</SelectItem>
            <SelectItem v-for="i in meta.instances" :key="i" :value="i">{{ i }}</SelectItem>
          </SelectContent>
        </Select>
        <div class="relative w-full max-w-sm">
          <Search class="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input v-model="query" class="h-8 pl-8" placeholder="Search name or org id (42 or prod-42)" />
        </div>
        <Button type="submit" variant="outline" size="sm" class="h-8">Search</Button>
        <Button v-if="applied" type="button" variant="ghost" size="sm" class="h-8" @click="clear">Clear</Button>
      </form>

      <div class="w-full overflow-x-auto rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Organisation</TableHead>
              <TableHead>Instance</TableHead>
              <TableHead class="w-56">Support tier</TableHead>
              <TableHead class="text-right">Open tickets</TableHead>
              <TableHead>Last active</TableHead>
              <TableHead>Tier set by</TableHead>
              <TableHead class="w-10"><span class="sr-only">History</span></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            <TableRow v-if="loading">
              <TableCell colspan="7" class="py-10 text-center text-muted-foreground">Loading…</TableCell>
            </TableRow>
            <TableRow v-else-if="!rows.length">
              <TableCell colspan="7" class="py-10 text-center text-muted-foreground">
                {{ applied || instance !== 'all' ? 'No organisations match.' : 'No organisations yet. They appear when their users first open Support.' }}
              </TableCell>
            </TableRow>
            <TableRow v-for="o in rows" v-else :key="o.org_id">
              <TableCell>
                <div class="font-medium">{{ o.org_name || '—' }}</div>
                <div class="text-xs text-muted-foreground">{{ o.org_id }}</div>
              </TableCell>
              <TableCell><Badge variant="outline">{{ o.instance }}</Badge></TableCell>
              <TableCell>
                <Select :model-value="o.tier || ''" @update:model-value="(t) => askTier(o, t)">
                  <SelectTrigger class="h-8"><SelectValue :placeholder="`${meta.default_tier} (default)`" /></SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="t in meta.tiers" :key="t" :value="t">{{ t }}</SelectItem>
                  </SelectContent>
                </Select>
              </TableCell>
              <TableCell class="text-right tabular-nums">{{ o.open_tickets }}</TableCell>
              <TableCell class="whitespace-nowrap text-sm" :title="o.last_seen_at ? full(o.last_seen_at) : ''">
                {{ o.last_seen_at ? short(o.last_seen_at) : 'Not yet' }}
              </TableCell>
              <TableCell class="whitespace-nowrap text-sm">
                <template v-if="o.tier_updated_at">{{ o.tier_updated_by || '—' }} <span class="text-muted-foreground">· {{ short(o.tier_updated_at) }}</span></template>
                <span v-else class="text-muted-foreground">{{ o.tier ? '—' : 'Default' }}</span>
              </TableCell>
              <TableCell>
                <Button variant="ghost" class="h-8 w-8 p-0" title="Tier history" aria-label="Tier history" @click="openHistory(o)">
                  <History class="h-4 w-4" />
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>

      <PaginationBar v-model:page="page" v-model:per-page="perPage" :total-pages="totalPages" />
    </div>

    <!-- Confirm a tier change -->
    <Dialog :open="!!pending" @update:open="(v) => { if (!v) pending = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Change {{ pending?.org.org_name }}'s support tier?</DialogTitle>
          <DialogDescription>
            From <strong>{{ pending?.org.tier || `${meta.default_tier} (default)` }}</strong> to <strong>{{ pending?.tier }}</strong>.
            New tickets from this organisation get the new tier and its SLA. Existing tickets keep theirs.
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" :disabled="busy" @click="pending = null">Cancel</Button>
          <Button :disabled="busy" @click="confirmTier">{{ busy ? 'Saving…' : 'Change tier' }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Tier history -->
    <Dialog :open="!!historyOrg" @update:open="(v) => { if (!v) historyOrg = null }">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Tier history: {{ historyOrg?.org_name }}</DialogTitle>
          <DialogDescription>{{ historyOrg?.org_id }}</DialogDescription>
        </DialogHeader>
        <p v-if="!history.length" class="text-sm text-muted-foreground">No changes yet; the default tier applies.</p>
        <ul v-else class="max-h-80 space-y-2 overflow-y-auto text-sm">
          <li v-for="(c, i) in history" :key="i" class="flex flex-wrap items-baseline justify-between gap-2 border-b pb-2 last:border-0">
            <span>{{ c.from_tier || `${meta.default_tier} (default)` }} → <strong>{{ c.to_tier }}</strong></span>
            <span class="text-xs text-muted-foreground">{{ c.actor_name }} · {{ full(c.created_at) }}</span>
          </li>
        </ul>
      </DialogContent>
    </Dialog>

    <!-- Add an organisation by hand -->
    <Dialog :open="adding" @update:open="(v) => (adding = v)">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add organisation</DialogTitle>
          <DialogDescription>Set a tier before the organisation's users first open Support. Use the org's id within its Encatch instance.</DialogDescription>
        </DialogHeader>
        <form id="encatch-add-org" class="grid gap-3" @submit.prevent="submitAdd">
          <div class="grid grid-cols-2 gap-3">
            <label class="grid gap-1 text-sm">Instance
              <Select v-model="form.instance">
                <SelectTrigger class="h-9"><SelectValue placeholder="Choose" /></SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="i in instanceChoices" :key="i" :value="i">{{ i }}</SelectItem>
                </SelectContent>
              </Select>
            </label>
            <label class="grid gap-1 text-sm">Org id
              <Input v-model="form.org_number" inputmode="numeric" placeholder="42" required />
            </label>
          </div>
          <label class="grid gap-1 text-sm">Name
            <Input v-model="form.org_name" placeholder="BigCorp" required maxlength="150" />
          </label>
          <label class="grid gap-1 text-sm">Support tier
            <Select v-model="form.tier">
              <SelectTrigger class="h-9"><SelectValue :placeholder="`${meta.default_tier} (default)`" /></SelectTrigger>
              <SelectContent>
                <SelectItem v-for="t in meta.tiers" :key="t" :value="t">{{ t }}</SelectItem>
              </SelectContent>
            </Select>
          </label>
        </form>
        <DialogFooter>
          <Button variant="outline" :disabled="busy" @click="adding = false">Cancel</Button>
          <Button type="submit" form="encatch-add-org" :disabled="busy || !form.instance || !form.org_number || !form.org_name">
            {{ busy ? 'Adding…' : 'Add' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, ref, watch } from 'vue'
import { format } from 'date-fns'
import { Search, Plus, History } from 'lucide-vue-next'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@shared-ui/components/ui/table'
import { Badge } from '@shared-ui/components/ui/badge'
import { Button } from '@shared-ui/components/ui/button'
import { Input } from '@shared-ui/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@shared-ui/components/ui/select'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@shared-ui/components/ui/dialog'
import PaginationBar from '@main/components/pagination/PaginationBar.vue'
import { useEmitter } from '@main/composables/useEmitter'
import { EMITTER_EVENTS } from '@main/constants/emitterEvents'
import { handleHTTPError } from '@shared-ui/utils/http.js'
import { listOrgs, getOrgsMeta, setOrgTier, addOrg, getOrgTierChanges } from './api'

const emitter = useEmitter()
const toastError = (error) => emitter.emit(EMITTER_EVENTS.SHOW_TOAST, { variant: 'destructive', description: handleHTTPError(error).message })

const meta = ref({ tiers: [], default_tier: 'SaaS Standard', instances: [] })
const rows = ref([])
const loading = ref(true)
const busy = ref(false)
const page = ref(1)
const perPage = ref(15)
const totalPages = ref(1)
const instance = ref('all')
const query = ref('')
const applied = ref('')

const short = (ts) => format(new Date(ts), 'd MMM yyyy')
const full = (ts) => format(new Date(ts), 'd MMM yyyy, HH:mm')

const load = async () => {
  loading.value = true
  try {
    const resp = await listOrgs({
      page: page.value, per_page: perPage.value,
      instance: instance.value === 'all' ? undefined : instance.value,
      search: applied.value || undefined
    })
    const d = resp.data.data
    rows.value = d.results || []
    totalPages.value = Math.max(1, d.total_pages || 0)
  } catch (error) {
    toastError(error)
  } finally {
    loading.value = false
  }
}
const loadMeta = async () => {
  try {
    meta.value = (await getOrgsMeta()).data.data
  } catch (error) {
    toastError(error)
  }
}
const search = () => {
  applied.value = query.value.trim()
  if (page.value !== 1) page.value = 1
  else load()
}
const clear = () => {
  query.value = ''
  instance.value = 'all'
  search()
}

// Tier change, confirmed in a dialog.
const pending = ref(null)
const askTier = (org, tier) => {
  if (!tier || tier === org.tier) return
  pending.value = { org, tier }
}
const confirmTier = async () => {
  busy.value = true
  try {
    await setOrgTier(pending.value.org.org_id, pending.value.tier)
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, { description: `${pending.value.org.org_name} is now ${pending.value.tier}. New tickets get it.` })
    pending.value = null
    await load()
  } catch (error) {
    toastError(error)
  } finally {
    busy.value = false
  }
}

// History.
const historyOrg = ref(null)
const history = ref([])
const openHistory = async (org) => {
  history.value = []
  historyOrg.value = org
  try {
    history.value = (await getOrgTierChanges(org.org_id)).data.data || []
  } catch (error) {
    toastError(error)
  }
}

// Add by hand.
const adding = ref(false)
const form = ref({ instance: 'prod', org_number: '', org_name: '', tier: '' })
const instanceChoices = computed(() => [...new Set(['prod', 'uat', 'dev', 'local', ...meta.value.instances])])
const openAdd = () => {
  form.value = { instance: 'prod', org_number: '', org_name: '', tier: '' }
  adding.value = true
}
const submitAdd = async () => {
  busy.value = true
  try {
    await addOrg({ ...form.value, org_number: String(form.value.org_number).trim() })
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, { description: `Added ${form.value.org_name}.` })
    adding.value = false
    await Promise.all([loadMeta(), load()])
  } catch (error) {
    toastError(error)
  } finally {
    busy.value = false
  }
}

watch([page, perPage], load)
onMounted(() => Promise.all([loadMeta(), load()]))
</script>
