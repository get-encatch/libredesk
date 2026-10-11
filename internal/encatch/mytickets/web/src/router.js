import { createRouter, createWebHistory } from 'vue-router'
import InboxView from './views/InboxView.vue'

// The list is where everyone lands; the ticket and new-ticket pages load when opened.
export const router = createRouter({
  history: createWebHistory('/my-tickets/'),
  routes: [
    { path: '/', name: 'list', component: InboxView },
    { path: '/new', name: 'new', component: () => import('./views/NewTicketView.vue') },
    { path: '/:ref', name: 'ticket', component: () => import('./views/TicketView.vue') },
  ],
})
