import { beforeEach, describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { state } from '@/session'
import InboxList from './InboxList.vue'

const Blank = { template: '<div />' }

function makeRouter() {
  return createRouter({
    history: createMemoryHistory('/my-tickets/'),
    routes: [
      { path: '/', name: 'list', component: Blank },
      { path: '/new', name: 'new', component: Blank },
      { path: '/:ref', name: 'ticket', component: Blank },
    ],
  })
}

const ticket = (ref, extra = {}) => ({
  ref, subject: `Subject ${ref}`, status: 'Open', sender: 'You', from_support: false, raised_by: 'Anita Rao',
  project_id: 'prod-17', project_name: 'Mobile app', preview: `Preview ${ref}`,
  created_at: '2026-10-09T02:53:25Z', updated_at: '2026-10-09T02:53:25Z', ...extra,
})

const list = (extra = {}) => ({
  tickets: [], status: '', project: '', project_name: '', search: '', page: 1, per_page: 50,
  has_newer: false, has_older: false, show_project: true, show_raised_by: true, ...extra,
})

async function render(props, path = '/') {
  const router = makeRouter()
  router.push(path)
  await router.isReady()
  const w = mount(InboxList, { props, global: { plugins: [router] } })
  await flushPromises()
  return { w, router }
}

beforeEach(() => {
  state.status = 'ready'
  state.session = { can_create: true, limits: { search: 100 } }
})

describe('InboxList', () => {
  it('shows the heading, count and rows', async () => {
    const { w } = await render({ wide: true, list: list({
      status: 'Open', project_name: 'Mobile app',
      tickets: [ticket('131', { status: 'Waiting on you', sender: 'Encatch Support', from_support: true }), ticket('128')],
    }) }, '/?status=Open&project=prod-17')
    expect(w.find('h1').text()).toBe('Open · Mobile app')
    expect(w.text()).toContain('2 tickets')
    const links = w.findAll('a[href^="/my-tickets/1"]').map((a) => a.attributes('href'))
    // Each ticket has a compact item and a wide row, both keeping the filters.
    expect(links).toEqual([
      '/my-tickets/131?status=Open&project=prod-17', '/my-tickets/131?status=Open&project=prod-17',
      '/my-tickets/128?status=Open&project=prod-17', '/my-tickets/128?status=Open&project=prod-17',
    ])
    expect(w.text()).toContain('Waiting on you')
    expect(w.find('[title="Waiting on your reply"]').exists()).toBe(true)
    expect(w.text()).toContain('Project')
    expect(w.text()).toContain('Raised by')
    expect(w.text()).toContain('ES') // the support avatar's initials
  })

  it('marks the open ticket and hides wide rows next to a ticket', async () => {
    const { w } = await render({ activeRef: '128', list: list({ tickets: [ticket('131'), ticket('128')] }) })
    expect(w.findAll('[aria-current="page"]').map((a) => a.attributes('href'))).toEqual(['/my-tickets/128'])
    expect(w.text()).not.toContain('Raised by') // no column header in the narrow list
  })

  it.each([
    [{}, 'No tickets yet', 'New ticket'],
    [{ search: 'zzz' }, 'No tickets match “zzz”', 'Clear search'],
    [{ status: 'Resolved' }, 'No tickets in this view', null],
    [{ has_newer: true, page: 3 }, 'No older tickets', null],
  ])('empty list %j', async (extra, title, button) => {
    const { w } = await render({ wide: true, list: list(extra) }, '/?status=Resolved&q=zzz')
    expect(w.text()).toContain(title)
    if (button) expect(w.text()).toContain(button)
    if (title.startsWith('No tickets match')) {
      expect(w.find('a[href="/my-tickets/?status=Resolved"]').text()).toBe('Clear search')
    }
  })

  it('offers no New ticket button to read-only users', async () => {
    state.session.can_create = false
    const { w } = await render({ wide: true, list: list() })
    expect(w.text()).toContain('No tickets yet')
    expect(w.text()).not.toContain('New ticket')
  })

  it('pages through the list, keeping the filters', async () => {
    const { w } = await render({ wide: true, list: list({ page: 2, has_newer: true, has_older: true, tickets: [ticket('100')] }) }, '/?status=Open&page=2')
    expect(w.text()).toContain('Page 2')
    expect(w.find('a[rel="prev"]').attributes('href')).toBe('/my-tickets/?status=Open')
    expect(w.find('a[rel="next"]').attributes('href')).toBe('/my-tickets/?status=Open&page=3')
  })

  it('searches within the current filters, from page 1', async () => {
    const { w, router } = await render({ wide: true, list: list() }, '/?status=Open&project=prod-17&page=4')
    await w.find('input[type="search"]').setValue('  export  ')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/?status=Open&project=prod-17&q=export')
  })

  it('shows a load error with a retry', async () => {
    const { w } = await render({ wide: true, list: null, error: "We couldn't load your tickets. Please try again." })
    expect(w.text()).toContain("We couldn't load your tickets.")
    await w.find('button').trigger('click')
    expect(w.emitted('retry')).toHaveLength(1)
  })
})
