<script setup>
import { Paperclip, X } from '@lucide/vue'
import { fileSize } from '@/uploads'

// Files chosen for a reply or a new ticket, each with its size and a remove button.
defineProps({ files: { type: Array, required: true } })
const emit = defineEmits(['remove'])
</script>

<template>
  <ul v-if="files.length" class="flex flex-wrap gap-2" aria-label="Files to attach">
    <li
      v-for="(f, i) in files"
      :key="`${f.name}-${i}`"
      class="inline-flex max-w-64 items-center gap-1.5 rounded-md border bg-muted/50 py-1 pl-2.5 pr-1 text-xs"
    >
      <Paperclip class="size-3.5 shrink-0 text-muted-foreground" />
      <span class="truncate">{{ f.name }}</span>
      <span class="shrink-0 text-muted-foreground">{{ fileSize(f.size) }}</span>
      <button
        type="button"
        class="rounded-sm p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
        :aria-label="`Remove ${f.name}`"
        @click="emit('remove', i)"
      >
        <X class="size-3.5" />
      </button>
    </li>
  </ul>
</template>
