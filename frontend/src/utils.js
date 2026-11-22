export const cpuCores = (milli) => (Number(milli || 0) / 1000).toFixed(milli % 1000 === 0 ? 0 : 1)
export const memGiB = (mi) => (Number(mi || 0) / 1024).toFixed(1)
export const gi = (v) => Number(v || 0).toFixed(0)

export function pct(used, limit) {
  if (!limit || limit <= 0) return 0
  return Math.min(100, Math.round((Number(used || 0) / Number(limit)) * 100))
}

export const statusMap = {
  running: { type: 'success', label: '运行中' },
  starting: { type: 'warning', label: '启动中' },
  pending: { type: 'info', label: '等待中' },
  stopped: { type: 'info', label: '已停止' },
  error: { type: 'danger', label: '异常' },
  creating: { type: 'warning', label: '创建中' }
}

export function statusOf(status) {
  return statusMap[status] || { type: 'info', label: status || '未知' }
}

export async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    const el = document.createElement('textarea')
    el.value = text
    document.body.appendChild(el)
    el.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(el)
    return ok
  }
}

export function fmtTime(t) {
  if (!t) return '-'
  const d = new Date(t)
  return d.toLocaleString('zh-CN', { hour12: false })
}