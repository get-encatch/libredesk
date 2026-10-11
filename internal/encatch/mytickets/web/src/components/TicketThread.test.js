import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import TicketThread from './TicketThread.vue'

const Blank = { template: '<div />' }

async function render(view, path = '/500') {
  const router = createRouter({
    history: createMemoryHistory('/my-tickets/'),
    routes: [{ path: '/', name: 'list', component: Blank }, { path: '/:ref', name: 'ticket', component: Blank }],
  })
  router.push(path)
  await router.isReady()
  const w = mount(TicketThread, { props: { view }, global: { plugins: [router] } })
  await flushPromises()
  return w
}

const msg = (n, extra = {}) => ({
  from_customer: n % 2 === 1, author: n % 2 ? 'Anita Rao' : 'Rahul Gorad', initials: n % 2 ? 'AR' : 'ES',
  html: `<p>Message ${n}</p>`, created_at: '2026-10-09T02:53:25Z', attachments: [], ...extra,
})

const view = (thread, messages, ticket = {}) => ({
  ticket: { ref: '500', subject: 'Long thread', status: 'Open', project_id: 'prod-17', project_name: 'Mobile app', raised_by: 'Ravi Kumar', raised_by_you: false, created_at: '2026-10-09T02:53:25Z', ...ticket },
  messages, thread: { page: 1, per_page: 10, total: messages.length, from: 1, to: messages.length, has_earlier: false, has_newer: false, ...thread },
  can_reply: true, org_id: 'prod-42',
})

describe('TicketThread', () => {
  it('pages through a long thread, keeping the list filters', async () => {
    const w = await render(view({ page: 2, total: 25, from: 6, to: 15, has_earlier: true, has_newer: true }, [msg(6), msg(7)]), '/500?status=Open&m=2')
    expect(w.text()).toContain('Messages 6–15 of 25')
    const links = Object.fromEntries(w.findAll('a').map((a) => [a.text(), a.attributes('href')]))
    expect(links['Show earlier messages']).toBe('/my-tickets/500?status=Open&m=3')
    expect(links['Show newer messages']).toBe('/my-tickets/500?status=Open')
  })

  it('marks the start of the conversation and hides paging for short threads', async () => {
    let w = await render(view({ page: 3, total: 25, from: 1, to: 5, has_newer: true }, [msg(1)]))
    expect(w.text()).toContain('Start of the conversation')
    expect(w.text()).not.toContain('Show earlier messages')
    w = await render(view({}, [msg(1), msg(2)]))
    expect(w.text()).not.toContain('Messages 1–2')
    expect(w.text()).not.toContain('Start of the conversation')
  })

  it('shows authors, the support badge, HTML and attachments', async () => {
    const w = await render(view({}, [msg(1), msg(2, { attachments: [{ name: 'fix.pdf', url: '/uploads/x?sig=y' }] })]))
    const [customer, support] = w.findAll('article')
    expect(customer.text()).toContain('Anita Rao')
    expect(customer.text()).not.toContain('Encatch Support')
    expect(support.text()).toContain('Encatch Support')
    expect(support.find('.message-body').html()).toContain('<p>Message 2</p>')
    const a = support.find('a[download]')
    expect(a.attributes('href')).toBe('/uploads/x?sig=y')
    expect(a.text()).toBe('fix.pdf')
  })

  it('asks for a reply when waiting on the customer, and says who raised it', async () => {
    const w = await render(view({}, [msg(1)], { status: 'Waiting on you' }))
    expect(w.text()).toContain("We're waiting for your reply.")
    expect(w.text()).toContain('#500 · opened 9 Oct 2026, 08:23 · Mobile app · raised by Ravi Kumar')
  })

  it('says when there are no messages', async () => {
    const w = await render(view({}, []))
    expect(w.text()).toContain('No messages yet.')
  })
})
