<script setup>
import { ref } from 'vue'
import { CircleCheck } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/api'

// "Mark as resolved": the customer closes a ticket they no longer need help with, with an
// optional note for the team (APIResolve). Replying reopens it.
const props = defineProps({ ticketRef: { type: String, required: true } })
const emit = defineEmits(['resolved'])

const MAX_NOTE = 1000 // api.go maxResolveNote
const open = ref(false)
const note = ref('')
const saving = ref(false)
const problem = ref('')

function onOpen(value) {
  open.value = value
  if (value) problem.value = ''
}

async function confirm() {
  if (saving.value) return
  saving.value = true
  problem.value = ''
  try {
    await api.resolve(props.ticketRef, note.value.trim())
    open.value = false
    note.value = ''
    emit('resolved')
  } catch (err) {
    if (err.code !== 'session_ended') problem.value = err.message
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Popover :open="open" @update:open="onOpen">
    <PopoverTrigger as-child>
      <Button variant="outline" size="sm" class="h-7 gap-1.5 px-2.5 text-xs">
        <CircleCheck class="size-3.5" />
        <span>Mark as resolved</span>
      </Button>
    </PopoverTrigger>
    <PopoverContent align="end" class="w-80 space-y-3 p-4">
      <form class="space-y-3" @submit.prevent="confirm">
        <div class="space-y-1">
          <p class="text-sm font-medium">Is your issue solved?</p>
          <p class="text-xs text-muted-foreground">
            We'll mark this ticket as resolved. If you need us again, just reply and it reopens.
          </p>
        </div>
        <Textarea
          v-model="note"
          :maxlength="MAX_NOTE"
          rows="3"
          placeholder="Anything to tell the team? (optional)"
          aria-label="Note for the support team (optional)"
          :disabled="saving"
        />
        <p v-if="problem" class="text-xs text-destructive" role="alert">{{ problem }}</p>
        <div class="flex justify-end gap-2">
          <Button type="button" variant="ghost" size="sm" :disabled="saving" @click="open = false">Cancel</Button>
          <Button type="submit" size="sm" :disabled="saving">{{ saving ? 'Resolving…' : 'Mark as resolved' }}</Button>
        </div>
      </form>
    </PopoverContent>
  </Popover>
</template>
