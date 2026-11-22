import axios from 'axios'
import { ElMessage } from 'element-plus'

const api = axios.create({ baseURL: '/api', timeout: 30000 })

api.interceptors.request.use((cfg) => {
  const token = localStorage.getItem('rp_token')
  if (token) cfg.headers.Authorization = `Bearer ${token}`
  return cfg
})

api.interceptors.response.use(
  (res) => res.data,
  (err) => {
    const status = err.response?.status
    const msg = err.response?.data?.error || err.message || '请求失败'
    if (status === 401) {
      localStorage.removeItem('rp_token')
      localStorage.removeItem('rp_user')
      if (location.pathname !== '/login') location.href = '/login'
    } else if (!err.config?.silent) {
      ElMessage.error(msg)
    }
    return Promise.reject(new Error(msg))
  }
)

export default api