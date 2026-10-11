import { defineComponent, h, ref } from 'vue'

// A stand-in for RichEditor in form tests: a textarea that emits what the editor would
// ({ html, text }), so tests can type with setValue. RichEditor.test.js tests the editor.
export const EditorStub = defineComponent({
  name: 'RichEditor',
  props: ['placeholder', 'labelledby', 'contentClass'],
  emits: ['update'],
  setup(props, { emit, expose }) {
    const value = ref('')
    expose({ clear: () => { value.value = '' }, focus: () => {} })
    return () => h('textarea', {
      class: 'editor-stub',
      placeholder: props.placeholder,
      value: value.value,
      onInput: (e) => {
        value.value = e.target.value
        const text = value.value.trim()
        emit('update', { html: text ? `<p>${text}</p>` : '', text })
      },
    })
  },
})
