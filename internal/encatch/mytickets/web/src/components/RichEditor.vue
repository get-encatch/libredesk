<script setup>
import { computed, onBeforeUnmount, ref, shallowRef } from 'vue'
import { Editor, EditorContent } from '@tiptap/vue-3'
import StarterKit from '@tiptap/starter-kit'
import { Placeholder } from '@tiptap/extensions'
import { Bold, Code, Italic, Link2, List, ListOrdered, SquareCode } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Toggle } from '@/components/ui/toggle'
import { linkHref } from '@/richText'

// A message editor with the formatting the server keeps (format.go): bold, italic,
// lists, inline code, code blocks and links (no quotes: the agent app hides them as
// quoted email). Pasted content is reduced to the same. Emits update with { html, text }; html is "" when the editor is empty.
const props = defineProps({
  placeholder: { type: String, default: '' },
  labelledby: { type: String, default: '' },
  // Classes for the editable area, e.g. its height: "[&_.tiptap]:min-h-44".
  contentClass: { type: String, default: '[&_.tiptap]:min-h-20' },
  disabled: { type: Boolean, default: false },
})
const emit = defineEmits(['update'])

// Bumped on every change, so the toolbar shows the formatting at the cursor.
const tick = ref(0)

const editor = shallowRef(new Editor({
  extensions: [
    StarterKit.configure({
      heading: false,
      blockquote: false,
      horizontalRule: false,
      strike: false,
      underline: false,
      link: {
        openOnClick: false,
        autolink: true,
        defaultProtocol: 'https',
        protocols: ['http', 'https', 'mailto'],
        HTMLAttributes: { rel: 'nofollow noopener', target: '_blank' },
      },
    }),
    Placeholder.configure({ placeholder: () => props.placeholder }),
  ],
  editorProps: {
    attributes: {
      class: 'message-body max-h-96 overflow-y-auto px-3 py-2 focus:outline-none',
      role: 'textbox',
      'aria-multiline': 'true',
      ...(props.labelledby ? { 'aria-labelledby': props.labelledby } : {}),
    },
  },
  editable: !props.disabled,
  onTransaction: () => tick.value++,
  onUpdate: ({ editor: e }) => {
    emit('update', {
      // Without the empty paragraph kept at the end for typing after a list or code.
      html: e.isEmpty ? '' : e.getHTML().replace(/(<p><\/p>)+$/, ''),
      text: e.getText({ blockSeparator: '\n' }).trim(),
    })
  },
}))

onBeforeUnmount(() => editor.value.destroy())

const active = (name) => {
  void tick.value
  return editor.value.isActive(name)
}

const marks = computed(() => [
  { name: 'bold', label: 'Bold', icon: Bold, run: (c) => c.toggleBold() },
  { name: 'italic', label: 'Italic', icon: Italic, run: (c) => c.toggleItalic() },
  { name: 'bulletList', label: 'Bulleted list', icon: List, run: (c) => c.toggleBulletList() },
  { name: 'orderedList', label: 'Numbered list', icon: ListOrdered, run: (c) => c.toggleOrderedList() },
  { name: 'code', label: 'Inline code', icon: Code, run: (c) => c.toggleCode() },
  { name: 'codeBlock', label: 'Code block', icon: SquareCode, run: (c) => c.toggleCodeBlock() },
])

function apply(m) {
  m.run(editor.value.chain().focus()).run()
}

// Links: edit the link at the cursor, or link the selection, or insert the address.
const linkOpen = ref(false)
const linkValue = ref('')
const linkProblem = ref('')

function openLink(open) {
  linkOpen.value = open
  if (open) {
    linkValue.value = editor.value.getAttributes('link').href || ''
    linkProblem.value = ''
  }
}

function saveLink() {
  const raw = linkValue.value.trim()
  const chain = editor.value.chain().focus().extendMarkRange('link')
  if (!raw) {
    chain.unsetLink().run()
    linkOpen.value = false
    return
  }
  const href = linkHref(raw)
  if (!href) {
    linkProblem.value = 'Use a web address (https://…) or an email address.'
    return
  }
  if (editor.value.state.selection.empty && !editor.value.isActive('link')) {
    // The address as the link's text, then a plain space so typing goes on outside it.
    chain.insertContent([{ type: 'text', text: raw, marks: [{ type: 'link', attrs: { href } }] }, { type: 'text', text: ' ' }]).run()
  } else {
    chain.setLink({ href }).run()
  }
  linkOpen.value = false
}

function removeLink() {
  editor.value.chain().focus().extendMarkRange('link').unsetLink().run()
  linkOpen.value = false
}

defineExpose({
  clear: () => editor.value.commands.clearContent(true),
  focus: () => editor.value.commands.focus('end'),
  editor,
})
</script>

<template>
  <div>
    <!-- mousedown.prevent: pressing a button mustn't take focus (and the cursor) from the text. -->
    <div class="flex flex-wrap items-center gap-0.5 border-b px-1.5 py-1" role="toolbar" aria-label="Formatting" @mousedown.prevent>
      <Toggle
        v-for="m in marks"
        :key="m.name"
        size="sm"
        class="size-7 min-w-7 px-0 text-muted-foreground data-[state=on]:text-foreground"
        :model-value="active(m.name)"
        :aria-label="m.label"
        :title="m.label"
        :disabled="disabled"
        @update:model-value="apply(m)"
      >
        <component :is="m.icon" class="size-4" />
      </Toggle>
      <Popover :open="linkOpen" @update:open="openLink">
        <PopoverTrigger as-child>
          <Toggle
            size="sm"
            class="size-7 min-w-7 px-0 text-muted-foreground data-[state=on]:text-foreground"
            :model-value="active('link')"
            aria-label="Link"
            title="Link"
            :disabled="disabled"
          >
            <Link2 class="size-4" />
          </Toggle>
        </PopoverTrigger>
        <!-- On closing, the cursor goes back into the text, not to the Link button. -->
        <PopoverContent align="start" class="w-80 space-y-2 p-3" @close-auto-focus.prevent="editor.commands.focus()">
          <form class="space-y-2" @submit.prevent="saveLink">
            <Input v-model="linkValue" placeholder="https://… or name@example.com" aria-label="Link address" />
            <p v-if="linkProblem" class="text-xs text-destructive" role="alert">{{ linkProblem }}</p>
            <div class="flex justify-end gap-2">
              <Button v-if="active('link')" type="button" variant="ghost" size="sm" @click="removeLink">Remove</Button>
              <Button type="submit" size="sm">Apply</Button>
            </div>
          </form>
        </PopoverContent>
      </Popover>
    </div>
    <EditorContent :editor="editor" :class="contentClass" />
  </div>
</template>
