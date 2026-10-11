<script setup>
import { Plus } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { state } from '@/session'
import AppSidebar from './AppSidebar.vue'
import Brand from './Brand.vue'
</script>

<template>
  <SidebarProvider :default-open="true">
    <AppSidebar />
    <SidebarInset class="min-w-0">
      <!-- Phones: a top bar; the sidebar opens as a sheet. -->
      <header class="flex h-14 shrink-0 items-center gap-2 border-b bg-sidebar px-3 md:hidden">
        <SidebarTrigger class="-ml-1" aria-label="Open the menu" />
        <Brand />
        <Button v-if="state.session.can_create" as-child size="sm" class="ml-auto">
          <RouterLink :to="{ name: 'new' }"><Plus />New</RouterLink>
        </Button>
      </header>
      <slot />
    </SidebarInset>
  </SidebarProvider>
</template>
