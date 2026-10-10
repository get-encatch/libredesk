<!--
  encatch: "Remove for security" menu on a message, for content shared by
  mistake (a password, key or sensitive file). Agents see it on their own
  messages; admins on every message, including customers'. Rendered by
  MessageBubble.vue; the backend is cmd/encatch_redact.go.
-->
<template>
  <div
    v-if="canRedact"
    class="flex-shrink-0 transition-opacity duration-200 can-hover:opacity-0 can-hover:group-hover:opacity-100 focus-within:!opacity-100"
    :class="{ 'order-last': !outgoing }"
  >
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button variant="ghost" class="w-8 h-8 p-0 text-muted-foreground" title="Remove for security" aria-label="Remove for security">
          <ShieldAlert class="w-4 h-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent :align="outgoing ? 'end' : 'start'" class="max-w-xs">
        <DropdownMenuLabel class="text-xs font-normal text-muted-foreground">Remove for security</DropdownMenuLabel>
        <DropdownMenuItem v-if="hasText" @click="ask({ kind: 'text' })">
          <TextCursorInput class="mr-2 h-4 w-4" />
          Remove message text…
        </DropdownMenuItem>
        <DropdownMenuSeparator v-if="hasText && attachments.length" />
        <DropdownMenuItem v-for="a in attachments" :key="a.uuid" @click="ask({ kind: 'attachment', attachment: a })">
          <Paperclip class="mr-2 h-4 w-4 shrink-0" />
          <span class="truncate">Remove {{ a.name }}…</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>

    <AlertDialog :open="!!pending" @update:open="(v) => { if (!v) pending = null }">
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>
            {{ pending?.kind === 'text' ? 'Remove this message’s text?' : `Remove ${pending?.attachment?.name}?` }}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {{ pending?.kind === 'text'
              ? 'The text and any images in it are deleted from the helpdesk and My Tickets now, and replaced with “[Removed for security]”.'
              : 'The file is deleted from the helpdesk and My Tickets now, and existing download links stop working.' }}
            Your reason is saved in the removal log and in a private note on this ticket. This can’t be undone.
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div v-if="exposure" class="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
          <p class="font-medium">{{ exposure === 'emailed' ? 'Already emailed to the customer' : 'The customer sent this' }}</p>
          <p class="mt-1 text-muted-foreground">
            {{ exposure === 'emailed'
              ? 'Removing it here doesn’t recall the email. Rotate any exposed password or key.'
              : 'Their own sent copy remains. Ask them to rotate any exposed password or key.' }}
            Backups keep older copies for about a month.
          </p>
        </div>

        <div class="space-y-2">
          <label for="encatch-redact-reason" class="text-sm font-medium">Why are you removing it?</label>
          <Textarea id="encatch-redact-reason" v-model="reason" rows="3" maxlength="500" required
            placeholder="e.g. The customer's live webhook secret was visible in the file" />
          <p class="text-xs" :class="reasonOK ? 'text-muted-foreground' : 'text-destructive'">
            {{ wordCount }} / {{ MIN_WORDS }} words minimum
          </p>
        </div>

        <AlertDialogFooter>
          <AlertDialogCancel :disabled="busy">Cancel</AlertDialogCancel>
          <Button variant="destructive" :disabled="busy || !reasonOK" @click="confirm">
            {{ busy ? 'Removing…' : 'Remove' }}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { ShieldAlert, Paperclip, TextCursorInput } from 'lucide-vue-next'
import { useConversationStore } from '@main/stores/conversation'
import { useUserStore } from '@main/stores/user'
import { useEmitter } from '@main/composables/useEmitter'
import { EMITTER_EVENTS } from '@main/constants/emitterEvents'
import { handleHTTPError } from '@shared-ui/utils/http.js'
import { Button } from '@shared-ui/components/ui/button'
import { Textarea } from '@shared-ui/components/ui/textarea'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger
} from '@shared-ui/components/ui/dropdown-menu'
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle
} from '@shared-ui/components/ui/alert-dialog'
import { removeAttachment, removeText } from './api'

const props = defineProps({
  message: { type: Object, required: true },
  outgoing: { type: Boolean, default: false }
})

const convStore = useConversationStore()
const userStore = useUserStore()
const emitter = useEmitter()

const MIN_WORDS = 5 // same rule as redact.CheckReason
const pending = ref(null)
const reason = ref('')
const busy = ref(false)

const m = computed(() => props.message)
const meta = computed(() => m.value.meta || {})
const attachments = computed(() => m.value.attachments || [])
const hasText = computed(() => !!m.value.content && !meta.value.encatch_text_removed)

// Same rules as the backend (internal/encatch/redact): incoming and outgoing
// messages only; agents their own, admins any.
const canRedact = computed(() => {
  if (!['incoming', 'outgoing'].includes(m.value.type)) return false
  if (meta.value.deleted_at || meta.value.is_csat) return false
  if (!hasText.value && !attachments.value.length) return false
  if (userStore.hasAdminRole) return true
  return m.value.type === 'outgoing' && m.value.sender_id === userStore.userID
})

// Where else the content may still exist (mirrors redact.Exposure).
const exposure = computed(() => {
  if (m.value.private) return ''
  if (m.value.type === 'incoming') return 'from_customer'
  return m.value.status === 'sent' ? 'emailed' : ''
})

const wordCount = computed(() => reason.value.trim().split(/\s+/).filter(Boolean).length)
const reasonOK = computed(() => wordCount.value >= MIN_WORDS)

const ask = (what) => {
  reason.value = ''
  pending.value = what
}

const confirm = async () => {
  const cuuid = convStore.current?.uuid
  if (!cuuid || !pending.value || !reasonOK.value) return
  busy.value = true
  try {
    const resp = pending.value.kind === 'text'
      ? await removeText(cuuid, m.value.uuid, reason.value)
      : await removeAttachment(cuuid, m.value.uuid, pending.value.attachment.uuid, reason.value)
    const { exposure: _, ...fields } = resp.data.data
    convStore.mergeMessageUpdate({ conversation_uuid: cuuid, uuid: m.value.uuid, ...fields })
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, { description: 'Removed. A private note records it.' })
    pending.value = null
  } catch (error) {
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, { variant: 'destructive', description: handleHTTPError(error).message })
  } finally {
    busy.value = false
  }
}
</script>
