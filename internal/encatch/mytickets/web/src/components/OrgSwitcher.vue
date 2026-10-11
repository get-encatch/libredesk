<script setup>
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { Check, ChevronsUpDown } from '@lucide/vue'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { state, switchOrg } from '@/session'

const router = useRouter()
const busy = ref(false)
const problem = ref('')

async function choose(id) {
  if (id === state.session.org.id || busy.value) return
  busy.value = true
  problem.value = ''
  try {
    await switchOrg(id)
    router.push({ name: 'list' })
  } catch (err) {
    problem.value = err.message
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div>
    <DropdownMenu v-if="state.session.orgs.length > 1">
      <DropdownMenuTrigger
        class="flex w-full items-center gap-2 rounded-md border bg-background px-3 py-2 text-left outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring/50"
        title="Switch organisation"
        :disabled="busy"
      >
        <span class="min-w-0 flex-1">
          <span class="block truncate text-sm font-medium">{{ state.session.org.name }}</span>
          <span class="block truncate text-xs text-muted-foreground">{{ state.session.org.access_label }}</span>
        </span>
        <ChevronsUpDown class="size-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="w-(--reka-dropdown-menu-trigger-width) min-w-56">
        <DropdownMenuLabel class="text-xs text-muted-foreground">Organisations</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem v-for="o in state.session.orgs" :key="o.id" @select="choose(o.id)">
          <span class="truncate">{{ o.name }}</span>
          <Check v-if="o.id === state.session.org.id" class="ml-auto" />
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    <div v-else class="rounded-md border bg-background px-3 py-2">
      <span class="block truncate text-sm font-medium">{{ state.session.org.name }}</span>
      <span class="block truncate text-xs text-muted-foreground">{{ state.session.org.access_label }}</span>
    </div>
    <p v-if="problem" class="mt-1 text-xs text-destructive" role="alert">{{ problem }}</p>
  </div>
</template>
