<script setup>
import { computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Folder, LogOut, Plus } from '@lucide/vue'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import {
  Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupLabel, SidebarHeader,
  SidebarMenu, SidebarMenuButton, SidebarMenuItem, SidebarSeparator, useSidebar,
} from '@/components/ui/sidebar'
import { filtersOf, listQuery, STATUSES } from '@/filters'
import { signOut, state } from '@/session'
import Brand from './Brand.vue'
import OrgSwitcher from './OrgSwitcher.vue'
import StatusIcon from './StatusIcon.vue'
import ThemeToggle from './ThemeToggle.vue'

const route = useRoute()
const { setOpenMobile } = useSidebar()
const s = computed(() => state.session)

// The list and a ticket's page both show the list, so both highlight its filter.
const onList = computed(() => route.name === 'list' || route.name === 'ticket')
const f = computed(() => filtersOf(route.query))

const statusLinks = computed(() => ['', ...STATUSES].map((status) => ({
  status,
  label: status || 'All tickets',
  to: { name: 'list', query: listQuery({ status, project: f.value.project }) },
  active: onList.value && f.value.status === status,
})))

// A project's link filters the list by it; clicking the current project shows all again.
const projectLinks = computed(() => s.value.projects.map((p) => {
  const active = onList.value && f.value.project === p.id
  return {
    ...p,
    active,
    to: { name: 'list', query: listQuery({ status: f.value.status, project: active ? '' : p.id }) },
  }
}))

// The phone menu is a sheet: close it once a link is followed.
watch(() => route.fullPath, () => setOpenMobile(false))
</script>

<template>
  <Sidebar>
    <SidebarHeader class="gap-3 px-3 pt-4">
      <div class="px-1">
        <Brand />
      </div>
      <OrgSwitcher />
      <Button v-if="s.can_create" as-child class="w-full">
        <RouterLink :to="{ name: 'new' }"><Plus />New ticket</RouterLink>
      </Button>
    </SidebarHeader>

    <SidebarContent>
      <SidebarGroup>
        <SidebarGroupLabel>Tickets</SidebarGroupLabel>
        <SidebarMenu>
          <SidebarMenuItem v-for="l in statusLinks" :key="l.label">
            <SidebarMenuButton as-child :is-active="l.active">
              <RouterLink :to="l.to" :aria-current="l.active ? 'page' : undefined">
                <StatusIcon :status="l.status" />
                <span>{{ l.label }}</span>
              </RouterLink>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarGroup>

      <SidebarGroup v-if="projectLinks.length">
        <SidebarGroupLabel>Projects</SidebarGroupLabel>
        <SidebarMenu>
          <SidebarMenuItem v-for="p in projectLinks" :key="p.id">
            <SidebarMenuButton as-child :is-active="p.active">
              <RouterLink :to="p.to" :aria-current="p.active ? 'true' : undefined" :title="p.active ? 'Show all projects' : undefined">
                <Folder />
                <span>{{ p.name }}</span>
              </RouterLink>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarGroup>
    </SidebarContent>

    <SidebarFooter class="gap-0 p-0">
      <SidebarSeparator class="mx-0" />
      <div class="px-2 py-1.5">
        <ThemeToggle />
      </div>
      <SidebarSeparator class="mx-0" />
      <div class="flex items-center gap-3 px-4 py-3">
        <Avatar class="size-8 rounded-lg">
          <AvatarFallback class="rounded-lg bg-primary text-xs font-semibold text-primary-foreground">{{ s.user.initials }}</AvatarFallback>
        </Avatar>
        <div class="min-w-0 flex-1 leading-tight">
          <p class="truncate text-sm font-medium">{{ s.user.name || s.user.email }}</p>
          <p class="truncate text-xs text-muted-foreground">{{ s.org.name }}</p>
        </div>
        <Button variant="ghost" size="icon-sm" class="text-muted-foreground hover:text-foreground" title="Sign out" aria-label="Sign out" @click="signOut">
          <LogOut />
        </Button>
      </div>
    </SidebarFooter>
  </Sidebar>
</template>
