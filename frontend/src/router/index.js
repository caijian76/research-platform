import { createRouter, createWebHistory } from 'vue-router'
import { useAuth } from '@/stores/auth'

const routes = [
  { path: '/login', component: () => import('@/views/Login.vue'), meta: { public: true } },
  { path: '/webssh/:id', name: 'webssh', component: () => import('@/views/WebSSH.vue') },
  {
    path: '/',
    component: () => import('@/components/AppLayout.vue'),
    children: [
      { path: '', name: 'dashboard', component: () => import('@/views/Dashboard.vue') },
      { path: 'environments', name: 'environments', component: () => import('@/views/Environments.vue') },
      { path: 'unit/:id', name: 'unit', component: () => import('@/views/UnitDetail.vue') },
      { path: 'units', name: 'units', component: () => import('@/views/Units.vue'), meta: { admin: true } },
      { path: 'users', name: 'users', component: () => import('@/views/Users.vue'), meta: { admin: true } },
      { path: 'templates', name: 'templates', component: () => import('@/views/Templates.vue'), meta: { admin: true } },
      { path: 'settings', name: 'settings', component: () => import('@/views/Settings.vue'), meta: { admin: true } },
      { path: 'password', name: 'password', component: () => import('@/views/Password.vue') }
    ]
  },
  { path: '/:pathMatch(.*)*', redirect: '/' }
]

const router = createRouter({ history: createWebHistory(), routes })

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  const token = localStorage.getItem('rp_token')
  if (!token) return { path: '/login', query: { redirect: to.fullPath } }
  const auth = useAuth()
  if (!auth.user) {
    try {
      await auth.fetchMe()
    } catch {
      auth.logout()
      return { path: '/login' }
    }
  }
  if (to.meta.admin && !auth.isAdmin) return { path: '/' }
  return true
})

export default router