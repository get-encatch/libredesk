import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { api, ApiError } from '@/api'
import { state } from '@/session'
import NewTicketView from './NewTicketView.vue'

const Blank = { template: '<div />' }
const uploads = { max_mb: 1, max_total_mb: 0, max_files: 5, extensions: ['pdf', 'txt'], accept: '.pdf,.txt' }

const options = (extra = {}) => ({
  projects: [{ id: 'prod-17', name: 'Mobile app' }, { id: 'prod-18', name: 'Website' }],
  allow_org_level: true, default_project: '', can_choose_priority: true,
  priorities: ['Normal', 'Express', 'Urgent'], uploads, email: 'anita@bigcorp.com', org_name: 'BigCorp', ...extra,
})

async function render() {
  const router = createRouter({
    history: createMemoryHistory('/my-tickets/'),
    routes: [{ path: '/', name: 'list', component: Blank }, { path: '/new', name: 'new', component: NewTicketView }, { path: '/:ref', name: 'ticket', component: Blank }],
  })
  router.push('/new')
  await router.isReady()
  const w = mount(NewTicketView, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

async function fill(w, subject, message) {
  await w.find('#subject').setValue(subject)
  await w.find('#message').setValue(message)
}

beforeEach(() => {
  state.status = 'ready'
  state.orgVersion = 0
  state.session = { org: { name: 'BigCorp' }, user: { email: 'anita@bigcorp.com' }, limits: { subject: 200, message: 20000, search: 100 } }
  Element.prototype.scrollIntoView = vi.fn()
})
afterEach(() => {
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

describe('NewTicketView', () => {
  it('shows the form with priorities and attachments for a manager', async () => {
    vi.spyOn(api, 'newTicket').mockResolvedValue(options())
    const { w } = await render()
    expect(w.text()).toContain("For BigCorp. We'll reply here and by email to anita@bigcorp.com.")
    expect(w.find('#project').text()).toBe('Not about a specific project')
    expect(w.findAll('[role="radio"]').map((r) => r.attributes('value'))).toEqual(['Normal', 'Express', 'Urgent'])
    expect(w.find('[role="radio"][data-state="checked"]').attributes('value')).toBe('Normal')
    expect(w.text()).toContain('Up to 5 files, 1 MB each.')
    expect(w.text()).toContain("Don't share passwords or secrets")
  })

  it('checks the form before sending', async () => {
    vi.spyOn(api, 'newTicket').mockResolvedValue(options())
    const create = vi.spyOn(api, 'createTicket')
    const { w } = await render()
    await w.find('form').trigger('submit')
    expect(w.find('[role="alert"]').text()).toBe('Please add a subject.')
    await w.find('#subject').setValue('Hi')
    await w.find('form').trigger('submit')
    expect(w.find('[role="alert"]').text()).toBe('Please describe the problem or question.')
    expect(create).not.toHaveBeenCalled()
  })

  it('creates an org-level ticket and opens it', async () => {
    vi.spyOn(api, 'newTicket').mockResolvedValue(options())
    const create = vi.spyOn(api, 'createTicket').mockResolvedValue({ ref: '142' })
    const { w, router } = await render()
    await fill(w, '  Export fails  ', 'Steps to reproduce')
    await w.find('form').trigger('submit')
    await flushPromises()
    const form = create.mock.calls[0][0]
    expect(Object.fromEntries(form.entries())).toEqual({ subject: 'Export fails', message: 'Steps to reproduce', project: '', priority: 'Normal' })
    expect(router.currentRoute.value.fullPath).toBe('/142')
  })

  it('defaults a project-only user to their project and sends no priority without the choice', async () => {
    vi.spyOn(api, 'newTicket').mockResolvedValue(options({ allow_org_level: false, default_project: 'prod-17', projects: [{ id: 'prod-17', name: 'Mobile app' }], can_choose_priority: false }))
    const create = vi.spyOn(api, 'createTicket').mockResolvedValue({ ref: '143' })
    const { w } = await render()
    expect(w.find('[role="radiogroup"]').exists()).toBe(false)
    expect(w.find('#project').text()).toBe('Mobile app')
    await fill(w, 'Crash', 'On launch')
    await w.find('form').trigger('submit')
    await flushPromises()
    const form = create.mock.calls[0][0]
    expect(form.get('project')).toBe('prod-17')
    expect(form.has('priority')).toBe(false)
  })

  it('refuses a disallowed file before uploading', async () => {
    vi.spyOn(api, 'newTicket').mockResolvedValue(options())
    const create = vi.spyOn(api, 'createTicket')
    const { w } = await render()
    const input = w.find('input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['MZ'], 'setup.exe')], configurable: true })
    await input.trigger('change')
    await fill(w, 'S', 'M')
    await w.find('form').trigger('submit')
    expect(w.find('[role="alert"]').text()).toContain('"setup.exe" can\'t be attached')
    expect(create).not.toHaveBeenCalled()
  })

  it('shows the server problem and keeps the form', async () => {
    vi.spyOn(api, 'newTicket').mockResolvedValue(options())
    vi.spyOn(api, 'createTicket').mockRejectedValue(new ApiError(500, 'server', "We couldn't create your ticket. Please try again."))
    const { w } = await render()
    await fill(w, 'Subject', 'Body')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(w.find('[role="alert"]').text()).toBe("We couldn't create your ticket. Please try again.")
    expect(w.find('#subject').element.value).toBe('Subject')
  })

  it('explains read-only access', async () => {
    vi.spyOn(api, 'newTicket').mockRejectedValue(new ApiError(403, 'read_only', "You can view this organisation's support tickets, but not raise new ones."))
    const { w } = await render()
    expect(w.text()).toContain('You have read-only access')
    expect(w.text()).toContain('but not raise new ones')
    expect(w.find('form').exists()).toBe(false)
  })
})
