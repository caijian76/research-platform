import { defineStore } from 'pinia'
import api from '@/api'

export const useAuth = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem('rp_token') || '',
    user: JSON.parse(localStorage.getItem('rp_user') || 'null'),
    memberships: []
  }),
  getters: {
    isAdmin: (s) => !!s.user?.is_platform_admin,
    unitAdminUnits: (s) => s.memberships.filter((m) => m.role === 'unit_admin')
  },
  actions: {
    async login(username, password) {
      const data = await api.post('/auth/login', { username, password })
      this.token = data.token
      this.user = data.user
      localStorage.setItem('rp_token', data.token)
      localStorage.setItem('rp_user', JSON.stringify(data.user))
      await this.fetchMe()
      return data.user
    },
    async fetchMe() {
      const data = await api.get('/me')
      this.user = data.user
      this.memberships = data.memberships || []
      localStorage.setItem('rp_user', JSON.stringify(data.user))
      return data
    },
    logout() {
      this.token = ''
      this.user = null
      this.memberships = []
      localStorage.removeItem('rp_token')
      localStorage.removeItem('rp_user')
    }
  }
})