<script setup>
import { computed, ref } from 'vue'
import { Paperclip, ShieldAlert } from '@lucide/vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/api'
import { STATUS_RESOLVED } from '@/filters'
import { state } from '@/session'
import { fileProblem, limitsText } from '@/uploads'
import FileChips from './FileChips.vue'

// The reply box under a ticket the customer may reply to. Checks the reply and its
// files as the server does, so problems show before anything is uploaded.
const props = defineProps({
  ticketRef: { type: String, required: true },
  status: { type: String, required: true },
})
const emit = defineEmits(['sent'])

const uploads = computed(() => state.session.uploads)
const maxLength = computed(() => state.session.limits.message)
const draft = ref('')
const files = ref([])
const problem = ref('')
const sent = ref(false)
const sending = ref(false)
const picker = ref(null)

const resolved = computed(() => props.status === STATUS_RESOLVED)

function addFiles(e) {
  files.value = [...files.value, ...e.target.files]
  e.target.value = '' // the same file can be chosen again after removing it
  problem.value = fileProblem(files.value, uploads.value)
}

function removeFile(i) {
  files.value = files.value.filter((_, j) => j !== i)
  problem.value = fileProblem(files.value, uploads.value)
}

async function send() {
  if (sending.value) return
  const body = draft.value.trim()
  sent.value = false
  problem.value = !body && !files.value.length
    ? 'Please write a reply or attach a file.'
    : body.length > maxLength.value
      ? `Please keep the reply under ${maxLength.value} characters.`
      : fileProblem(files.value, uploads.value)
  if (problem.value) return

  const form = new FormData()
  form.append('message', body)
  for (const f of files.value) form.append('files', f)
  sending.value = true
  try {
    await api.reply(props.ticketRef, form)
    draft.value = ''
    files.value = []
    sent.value = true
    emit('sent')
  } catch (err) {
    if (err.code !== 'session_ended') problem.value = err.message
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <form class="border-t px-4 py-3 lg:px-6" @submit.prevent="send">
    <Alert v-if="problem" variant="destructive" class="mb-3" role="alert">
      <AlertDescription>{{ problem }}</AlertDescription>
    </Alert>
    <Alert v-else-if="sent" class="mb-3" role="status">
      <AlertDescription class="text-foreground">Your reply was sent. We'll get back to you here and by email.</AlertDescription>
    </Alert>

    <label for="message" class="sr-only">{{ resolved ? 'Reply to reopen this ticket' : 'Reply' }}</label>
    <div class="rounded-lg border border-input bg-background focus-within:ring-1 focus-within:ring-ring">
      <Textarea
        id="message"
        v-model="draft"
        name="message"
        rows="3"
        :maxlength="maxLength"
        :placeholder="resolved ? 'Reply to reopen this ticket…' : 'Write your reply…'"
        class="min-h-0 resize-none rounded-none border-0 bg-transparent px-3 pt-3 shadow-none focus-visible:ring-0 dark:bg-transparent"
      />
      <FileChips :files="files" class="px-3 pb-2" @remove="removeFile" />
      <div class="flex flex-wrap items-center justify-between gap-3 px-3 pb-3">
        <template v-if="uploads">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            class="text-muted-foreground hover:text-foreground"
            :title="limitsText(uploads)"
            @click="picker.click()"
          >
            <Paperclip /><span class="sr-only">Attach files</span>
          </Button>
          <input ref="picker" type="file" name="files" multiple class="hidden" :accept="uploads.accept || undefined" @change="addFiles">
        </template>
        <span v-else class="text-xs text-muted-foreground">We'll also email you when we reply.</span>
        <Button type="submit" size="sm" class="ml-auto" :disabled="sending">{{ sending ? 'Sending…' : 'Send reply' }}</Button>
      </div>
    </div>
    <p class="mt-2 flex items-start gap-1.5 text-xs text-muted-foreground">
      <ShieldAlert class="mt-px size-3.5 shrink-0 text-warning-foreground" />
      <span>Don't include passwords, API keys or tokens in replies or files. We'll never ask for your password.</span>
    </p>
  </form>
</template>
