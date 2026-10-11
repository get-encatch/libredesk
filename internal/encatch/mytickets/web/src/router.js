import { createRouter, createWebHistory } from 'vue-router'
import InboxView from './views/InboxView.vue'
import TicketView from './views/TicketView.vue'
import NewTicketView from './views/NewTicketView.vue'

export const router = createRouter({
  history: createWebHistory('/my-tickets/'),
  routes: [
    { path: '/', name: 'list', component: InboxView },
    { path: '/new', name: 'new', component: NewTicketView },
    { path: '/:ref', name: 'ticket', component: TicketView },
  ],
})
