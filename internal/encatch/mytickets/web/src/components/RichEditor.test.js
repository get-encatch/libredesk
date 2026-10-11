import { afterEach, describe, expect, it } from 'vitest'
import { DOMWrapper, flushPromises, mount } from '@vue/test-utils'
import RichEditor from './RichEditor.vue'

// jsdom lacks layout; ProseMirror only asks for it when scrolling the cursor into view.
document.elementFromPoint ??= () => null
Range.prototype.getClientRects ??= () => []
Range.prototype.getBoundingClientRect ??= () => ({ top: 0, left: 0, bottom: 0, right: 0, width: 0, height: 0 })

let w
// The editing area is attached once mounted, hence async.
async function render(props = {}) {
  w = mount(RichEditor, { props: { placeholder: 'Write…', labelledby: 'lbl', ...props }, attachTo: document.body })
  await flushPromises()
  return w
}
const editor = () => w.vm.editor
const last = () => w.emitted('update').at(-1)[0]
const button = (label) => w.find(`[aria-label="${label}"]`)

afterEach(() => {
  w?.unmount()
  document.body.innerHTML = ''
})

describe('RichEditor', () => {
  it('labels the text area and offers the allowed formatting only', async () => {
    await render()
    const area = w.find('.tiptap')
    expect(area.attributes('aria-labelledby')).toBe('lbl')
    expect(area.attributes('role')).toBe('textbox')
    expect(w.findAll('[role="toolbar"] button').map((b) => b.attributes('aria-label'))).toEqual([
      'Bold', 'Italic', 'Bulleted list', 'Numbered list', 'Inline code', 'Code block', 'Link',
    ])
    // No headings, quotes, rules, strike or underline: the server wouldn't keep them.
    const names = Object.keys(editor().schema.nodes).concat(Object.keys(editor().schema.marks))
    for (const off of ['heading', 'blockquote', 'horizontalRule', 'strike', 'underline']) expect(names).not.toContain(off)
  })

  it('emits html and text as the customer types and formats', async () => {
    await render()
    editor().commands.setContent('<p>Export fails</p>', { emitUpdate: true })
    editor().commands.selectAll()
    await button('Bold').trigger('click')
    expect(last()).toEqual({ html: '<p><strong>Export fails</strong></p>', text: 'Export fails' })
    expect(button('Bold').attributes('data-state')).toBe('on')

    editor().commands.focus('end')
    await button('Bulleted list').trigger('click')
    expect(last().html).toBe('<ul><li><p><strong>Export fails</strong></p></li></ul>')
    await button('Bulleted list').trigger('click')
    await button('Code block').trigger('click')
    expect(last().html).toBe('<pre><code>Export fails</code></pre>')
  })

  it('reduces pasted or loaded content to the allowed formatting', async () => {
    await render()
    editor().commands.setContent('<h1>Title</h1><p style="color:red">Text <u>under</u> <s>gone</s></p><hr><blockquote><p>quoted</p></blockquote><img src="x.png"><table><tr><td>cell</td></tr></table>', { emitUpdate: true })
    const { html, text } = last()
    expect(html).not.toMatch(/<h1|<u>|<s>|<hr|<blockquote|<img|<table|style=/)
    expect(text).toContain('quoted')
    expect(text).toContain('Title')
    expect(text).toContain('cell')
  })

  it('emits nothing to send when empty, and drops the trailing empty paragraph', async () => {
    await render()
    editor().commands.setContent('<p>Hi</p><p></p><p></p>', { emitUpdate: true })
    expect(last().html).toBe('<p>Hi</p>')
    editor().commands.setContent('<p></p>', { emitUpdate: true })
    expect(last()).toEqual({ html: '', text: '' })
  })

  it('shows a new placeholder at once', async () => {
    await render({ placeholder: 'Reply to reopen this ticket…' })
    const shown = () => document.querySelector('.tiptap p').getAttribute('data-placeholder')
    expect(shown()).toBe('Reply to reopen this ticket…')
    await w.setProps({ placeholder: 'Write your reply…' })
    expect(shown()).toBe('Write your reply…')
  })

  it('clears through its exposed clear()', async () => {
    await render()
    editor().commands.setContent('<p>Draft</p>', { emitUpdate: true })
    w.vm.clear()
    expect(last()).toEqual({ html: '', text: '' })
    expect(editor().isEmpty).toBe(true)
  })

  it('adds links through the link box, refusing unsafe addresses', async () => {
    await render()
    editor().commands.setContent('<p>Docs: </p>', { emitUpdate: true })
    // Cursor at the end, without focusing: in jsdom a focused editor swallows the
    // first synthetic click on the Link button (real clicks open it, see the browser checks).
    editor().commands.setTextSelection(editor().state.doc.content.size - 1)
    await button('Link').trigger('click')
    await flushPromises()
    const input = new DOMWrapper(document.querySelector('[aria-label="Link address"]'))
    const submit = () => new DOMWrapper(input.element.closest('form')).trigger('submit')

    await input.setValue('javascript:alert(1)')
    await submit()
    await flushPromises()
    expect(document.body.textContent).toContain('Use a web address')
    expect(last().html).not.toContain('<a')

    await input.setValue('encatch.com/docs')
    await submit()
    await flushPromises()
    // (Loading "<p>Docs: </p>" trims its trailing space; typing keeps it, as in the browser checks.)
    expect(last().html).toBe('<p>Docs:<a target="_blank" rel="nofollow noopener" href="https://encatch.com/docs">encatch.com/docs</a> </p>')
  })
})
