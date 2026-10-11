<script setup>
import { computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Skeleton } from '@/components/ui/skeleton'
import AppShell from '@/components/AppShell.vue'
import MessageCard from '@/components/MessageCard.vue'
import { state } from '@/session'
import { signInProblem } from '@/signin'

const route = useRoute()
const router = useRouter()

// Why sign-in failed, when the server sent us here to explain it.
const signIn = computed(() => signInProblem(route.query))

// With a session from an earlier sign-in the failure doesn't matter: drop the reason
// from the URL and show the tickets.
watch(() => [state.status, route.query.signin], ([status, reason]) => {
  if (status === 'ready' && reason !== undefined) {
    const { signin, ...rest } = route.query
    router.replace({ query: rest })
  }
})
</script>

<template>
  <MessageCard v-if="state.status === 'ended' && signIn" title="Something's not right" :text="signIn" />
  <MessageCard v-else-if="state.status === 'ended'" title="Your session has ended" :text="state.message" />
  <MessageCard
    v-else-if="state.status === 'signed-out'"
    title="Signed out"
    text="You've signed out of Encatch Support. Open Support from your Encatch app to sign in again."
  />
  <MessageCard v-else-if="state.status === 'error'" title="Something's not right" :text="state.message" />
  <AppShell v-else-if="state.status === 'ready'">
    <RouterView />
  </AppShell>
  <div v-else class="flex min-h-screen" aria-busy="true" aria-label="Loading">
    <div class="hidden w-64 shrink-0 space-y-3 border-r bg-sidebar p-4 md:block">
      <Skeleton class="h-6 w-32" />
      <Skeleton class="h-12 w-full" />
      <Skeleton class="h-9 w-full" />
    </div>
    <div class="flex-1 space-y-3 p-6">
      <Skeleton class="h-6 w-40" />
      <Skeleton class="h-4 w-full" />
      <Skeleton class="h-4 w-5/6" />
    </div>
  </div>
</template>
