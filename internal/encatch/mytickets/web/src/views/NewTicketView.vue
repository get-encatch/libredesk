<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ShieldAlert } from '@lucide/vue'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator,
} from '@/components/ui/breadcrumb'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Textarea } from '@/components/ui/textarea'
import FileChips from '@/components/FileChips.vue'
import { api } from '@/api'
import { newTicketProblem, ORG_LEVEL, PRIORITIES } from '@/newTicket'
import { state } from '@/session'
import { useTitle } from '@/title'
import { fileProblem, limitsText } from '@/uploads'

useTitle(() => 'New ticket')
const router = useRouter()

// What the form offers this session (projects, priority, uploads), from the server.
const options = ref(null)
const readOnly = ref('') // the server's explanation, when the user can't raise tickets
const loadError = ref('')

const form = ref({ project: '', subject: '', message: '', priority: 'Normal' })
const files = ref([])
const problem = ref('')
const sending = ref(false)
const picker = ref(null)
const top = ref(null)

async function load() {
  readOnly.value = ''
  loadError.value = ''
  try {
    const o = await api.newTicket()
    options.value = o
    // The only project when org-level tickets aren't an option, else org level.
    form.value.project = o.default_project || (o.allow_org_level ? ORG_LEVEL : '')
  } catch (err) {
    options.value = null
    if (err.code === 'read_only') readOnly.value = err.message
    else if (err.code !== 'session_ended') loadError.value = err.message
  }
}
// Again when the org changes: its projects and access differ.
watch(() => state.orgVersion, load, { immediate: true })

const limits = computed(() => state.session.limits)
const uploads = computed(() => options.value?.uploads)
const project = computed(() => (form.value.project === ORG_LEVEL ? '' : form.value.project))

function addFiles(e) {
  files.value = [...files.value, ...e.target.files]
  e.target.value = ''
  problem.value = fileProblem(files.value, uploads.value)
}

function removeFile(i) {
  files.value = files.value.filter((_, j) => j !== i)
  problem.value = fileProblem(files.value, uploads.value)
}

async function showProblem(text) {
  problem.value = text
  await nextTick()
  top.value?.scrollIntoView({ block: 'start', behavior: 'smooth' })
}

async function submit() {
  if (sending.value) return
  const subject = form.value.subject.trim()
  const message = form.value.message.trim()
  const p = newTicketProblem({ subject, message, project: project.value }, options.value, limits.value) || fileProblem(files.value, uploads.value)
  if (p) return showProblem(p)

  const body = new FormData()
  body.append('subject', subject)
  body.append('message', message)
  body.append('project', project.value)
  if (options.value.can_choose_priority) body.append('priority', form.value.priority)
  for (const f of files.value) body.append('files', f)
  sending.value = true
  try {
    const { ref: created } = await api.createTicket(body)
    router.push({ name: 'ticket', params: { ref: created } })
  } catch (err) {
    if (err.code === 'read_only') readOnly.value = err.message
    else if (err.code !== 'session_ended') showProblem(err.message)
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <div ref="top" class="mx-auto max-w-3xl px-4 pb-16 pt-6 md:px-8 md:pt-10">
    <Card v-if="readOnly" class="mx-auto mt-10 max-w-md">
      <CardHeader>
        <CardTitle class="text-xl">You have read-only access</CardTitle>
        <CardDescription class="leading-relaxed">{{ readOnly }}</CardDescription>
      </CardHeader>
    </Card>

    <template v-else>
      <Breadcrumb class="mb-3">
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbLink as-child><RouterLink :to="{ name: 'list' }">Tickets</RouterLink></BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem><BreadcrumbPage>New ticket</BreadcrumbPage></BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>
      <div class="mb-6 space-y-1">
        <h1 class="text-2xl font-semibold tracking-tight">New ticket</h1>
        <p class="text-sm text-muted-foreground">
          For {{ state.session.org.name }}. We'll reply here and by email to {{ state.session.user.email }}.
        </p>
      </div>

      <Alert v-if="problem || loadError" variant="destructive" class="mb-4" role="alert">
        <AlertDescription>{{ problem || loadError }}</AlertDescription>
      </Alert>

      <div v-if="!options && !loadError" class="space-y-4" aria-busy="true">
        <Skeleton class="h-9 w-full" />
        <Skeleton class="h-9 w-full" />
        <Skeleton class="h-44 w-full" />
      </div>

      <Card v-else-if="options">
        <CardContent>
          <form class="space-y-6" novalidate @submit.prevent="submit">
            <div v-if="options.projects.length" class="space-y-2">
              <Label for="project">Project</Label>
              <Select v-model="form.project">
                <SelectTrigger id="project" class="w-full">
                  <SelectValue placeholder="Choose a project" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-if="options.allow_org_level" :value="ORG_LEVEL">Not about a specific project</SelectItem>
                  <SelectItem v-for="p in options.projects" :key="p.id" :value="p.id">{{ p.name }}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div class="space-y-2">
              <Label for="subject">Subject</Label>
              <Input
                id="subject"
                v-model="form.subject"
                name="subject"
                :maxlength="limits.subject"
                placeholder="A short summary of the issue"
              />
            </div>

            <div class="space-y-2">
              <Label for="message">What do you need help with?</Label>
              <Textarea id="message" v-model="form.message" name="message" :maxlength="limits.message" class="min-h-44" />
              <p class="text-xs text-muted-foreground">For bugs, include the steps to reproduce, what you expected, and the SDK and version.</p>
            </div>

            <Alert class="border-warning-foreground bg-warning" role="note">
              <ShieldAlert class="text-warning-foreground" />
              <AlertTitle>Don't share passwords or secrets</AlertTitle>
              <AlertDescription class="text-xs leading-relaxed text-foreground">
                Leave out passwords, API keys, tokens and card numbers, in your message and in files. Remove them from logs and screenshots before attaching. Encatch Support will never ask for your password.
              </AlertDescription>
            </Alert>

            <div v-if="uploads" class="space-y-2" role="group" aria-labelledby="files-label">
              <Label id="files-label" as="p">Attachments <span class="font-normal text-muted-foreground">(optional)</span></Label>
              <div class="flex flex-wrap items-center gap-2">
                <Button type="button" variant="outline" size="sm" @click="picker.click()">Choose files</Button>
                <input ref="picker" type="file" name="files" multiple class="hidden" :accept="uploads.accept || undefined" @change="addFiles">
                <FileChips :files="files" @remove="removeFile" />
              </div>
              <p class="text-xs text-muted-foreground">Screenshots, logs or recordings help. {{ limitsText(uploads) }}.</p>
            </div>

            <fieldset v-if="options.can_choose_priority" class="space-y-3">
              <legend class="text-sm font-medium leading-none">Priority</legend>
              <RadioGroup v-model="form.priority" class="grid gap-3 sm:grid-cols-3" aria-label="Priority">
                <Label
                  v-for="p in options.priorities"
                  :key="p"
                  class="flex cursor-pointer items-start gap-3 rounded-xl border bg-card p-4 font-normal transition-colors hover:bg-accent has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:ring-1 has-[[data-state=checked]]:ring-primary"
                >
                  <RadioGroupItem :value="p" class="mt-0.5" />
                  <span>
                    <span class="block text-sm font-medium">{{ p }}</span>
                    <span class="text-xs text-muted-foreground">{{ PRIORITIES[p] }}</span>
                  </span>
                </Label>
              </RadioGroup>
            </fieldset>

            <div class="flex justify-end">
              <Button type="submit" :disabled="sending">{{ sending ? 'Submitting…' : 'Submit ticket' }}</Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </template>
  </div>
</template>
