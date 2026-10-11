<script setup>
import InboxList from '@/components/InboxList.vue'
import { useTickets } from '@/tickets'
import { useTitle } from '@/title'

const { list, loading, error, reload } = useTickets()
useTitle(() => 'Your tickets')
</script>

<template>
  <div class="lg:flex lg:h-svh lg:overflow-hidden">
    <InboxList wide :list="list" :loading="loading" :error="error" @retry="reload" />
  </div>
</template>
