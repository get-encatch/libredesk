import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { api, ApiError } from '@/api'
import { state } from '@/session'
import ReplyBox from './ReplyBox.vue'
import { EditorStub } from '@/test/editorStub'

const uploads = { max_mb: 1, max_total_mb: 0, max_files: 5, extensions: ['pdf', 'txt'], accept: '.pdf,.txt' }

function render(status = 'Open') {
  return mount(ReplyBox, { props: { ticketRef: '131', status }, attachTo: document.body, global: { stubs: { RichEditor: EditorStub } } })
}

function choose(w, files) {
  const input = w.find('input[type="file"]')
  Object.defineProperty(input.element, 'files', { value: files, configurable: true })
  return input.trigger('change')
}

beforeEach(() => {
  state.session = { uploads, limits: { message: 20000 } }
})
afterEach(() => {
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('ReplyBox', () => {
  it('needs a reply or a file', async () => {
    const reply = vi.spyOn(api, 'reply')
    const w = render()
    await w.find('form').trigger('submit')
    expect(w.text()).toContain('Please write a reply or attach a file.')
    expect(reply).not.toHaveBeenCalled()
  })

  it('refuses a file before uploading it, and forgets it when removed', async () => {
    const reply = vi.spyOn(api, 'reply')
    const w = render()
    await choose(w, [new File(['MZ'], 'setup.exe')])
    expect(w.text()).toContain('"setup.exe" can\'t be attached')
    await w.find('.editor-stub').setValue('see file')
    await w.find('form').trigger('submit')
    expect(reply).not.toHaveBeenCalled()
    await w.find('button[aria-label="Remove setup.exe"]').trigger('click')
    expect(w.text()).not.toContain('setup.exe')
  })

  it('sends the reply and files, then clears and confirms', async () => {
    const reply = vi.spyOn(api, 'reply').mockResolvedValue({ sent: true })
    const w = render()
    await w.find('.editor-stub').setValue('  Logs attached  ')
    await choose(w, [new File(['log'], 'app.txt')])
    await w.find('form').trigger('submit')
    await flushPromises()
    const [ref, form] = reply.mock.calls[0]
    expect(ref).toBe('131')
    expect(form.get('message')).toBe('Logs attached')
    expect(form.get('message_html')).toBe('<p>Logs attached</p>')
    expect(form.getAll('files').map((f) => f.name)).toEqual(['app.txt'])
    expect(w.emitted('sent')).toHaveLength(1)
    expect(w.find('.editor-stub').element.value).toBe('')
    expect(w.text()).toContain('Your reply was sent.')
    expect(w.text()).not.toContain('app.txt')
  })

  it('keeps the draft and shows the server problem', async () => {
    vi.spyOn(api, 'reply').mockRejectedValue(new ApiError(403, 'read_only', "You have read-only access to this ticket, so you can't reply."))
    const w = render()
    await w.find('.editor-stub').setValue('hello')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.text()).toContain('read-only access')
    expect(w.find('.editor-stub').element.value).toBe('hello')
    expect(w.emitted('sent')).toBeUndefined()
  })

  it('invites a reply to reopen a resolved ticket', () => {
    expect(render('Resolved').find('.editor-stub').attributes('placeholder')).toBe('Reply to reopen this ticket…')
    expect(render('Open').find('.editor-stub').attributes('placeholder')).toBe('Write your reply…')
  })

  it('offers no attachments when they are off', () => {
    state.session.uploads = null
    const w = render()
    expect(w.find('input[type="file"]').exists()).toBe(false)
    expect(w.text()).toContain("We'll also email you when we reply.")
  })
})
