<script setup>
import { Moon, Sun } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { isDark, toggleTheme } from '@/theme'
</script>

<template>
  <div class="flex items-center justify-between gap-2 px-2 py-1">
    <span class="text-xs font-medium text-muted-foreground">Appearance</span>
    <Button
      variant="ghost"
      size="icon-sm"
      class="text-muted-foreground hover:text-foreground"
      title="Switch between light and dark"
      aria-label="Switch between light and dark"
      @click="toggleTheme"
    >
      <Sun v-if="isDark" />
      <Moon v-else />
    </Button>
  </div>
</template>
