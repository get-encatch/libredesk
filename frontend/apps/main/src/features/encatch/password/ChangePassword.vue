<template>
  <!-- encatch: Account > Password (cmd/encatch_password.go). -->
  <div class="h-full">
    <form class="flex max-w-md flex-col space-y-5" @submit.prevent="save" novalidate>
      <div class="space-y-1">
        <span class="sub-title">Change password</span>
        <p class="text-muted-foreground text-xs">
          For signing in with your email and password. If you sign in with SSO, your password is
          managed by your SSO provider.
        </p>
      </div>

      <div class="space-y-2">
        <Label for="current-password">Current password</Label>
        <Input id="current-password" v-model="current" type="password" autocomplete="current-password" :disabled="saving" />
        <p class="text-muted-foreground text-xs">
          Forgot it? Sign out and use "Forgot password" on the sign-in page.
        </p>
      </div>

      <div class="space-y-2">
        <Label for="new-password">New password</Label>
        <Input id="new-password" v-model="next" type="password" autocomplete="new-password" :disabled="saving" />
        <ul class="space-y-1 text-xs" aria-label="Password requirements">
          <li
            v-for="rule in rules"
            :key="rule.label"
            class="flex items-center gap-1.5"
            :class="rule.ok ? 'text-green-600 dark:text-green-500' : 'text-muted-foreground'"
          >
            <Check v-if="rule.ok" class="size-3.5" aria-hidden="true" />
            <Circle v-else class="size-3.5" aria-hidden="true" />
            <span>{{ rule.label }}</span>
            <span class="sr-only">{{ rule.ok ? '(met)' : '(not met)' }}</span>
          </li>
        </ul>
      </div>

      <div class="space-y-2">
        <Label for="confirm-password">Confirm new password</Label>
        <Input id="confirm-password" v-model="confirm" type="password" autocomplete="new-password" :disabled="saving" />
        <p v-if="confirm && confirm !== next" class="text-destructive text-xs" role="alert">
          The passwords don't match.
        </p>
      </div>

      <p v-if="problem" class="text-destructive text-sm" role="alert">{{ problem }}</p>

      <Button type="submit" class="self-start" :isLoading="saving" :disabled="!ready || saving">
        Change password
      </Button>
    </form>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { Check, Circle } from 'lucide-vue-next'
import { Button } from '@shared-ui/components/ui/button'
import { Input } from '@shared-ui/components/ui/input'
import { Label } from '@shared-ui/components/ui/label'
import { handleHTTPError } from '@shared-ui/utils/http.js'
import { useEmitter } from '@main/composables/useEmitter'
import { EMITTER_EVENTS } from '@main/constants/emitterEvents'
import { changeOwnPassword } from './api'
import { isStrongPassword, passwordRules } from './rules'

const emitter = useEmitter()
const current = ref('')
const next = ref('')
const confirm = ref('')
const saving = ref(false)
const problem = ref('')

const rules = computed(() => passwordRules.map((r) => ({ label: r.label, ok: r.test(next.value) })))
const ready = computed(
  () => current.value !== '' && isStrongPassword(next.value) && confirm.value === next.value
)

async function save () {
  if (!ready.value || saving.value) return
  if (next.value === current.value) {
    problem.value = 'The new password must be different from the current one.'
    return
  }
  problem.value = ''
  saving.value = true
  try {
    await changeOwnPassword(current.value, next.value)
    current.value = next.value = confirm.value = ''
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, { description: 'Password changed.' })
  } catch (error) {
    problem.value = handleHTTPError(error).message
  } finally {
    saving.value = false
  }
}
</script>
