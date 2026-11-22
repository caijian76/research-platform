<template>
  <div class="webssh-page">
    <header class="ws-head">
      <div class="ws-title">
        <el-icon :size="20"><Terminal /></el-icon>
        <div>
          <div class="ws-name">{{ env?.name || 'WebSSH 终端' }}</div>
          <div class="ws-sub">
            <template v-if="env">{{ env.unit_name }} · 命名空间 {{ env.namespace }}</template>
            <template v-else>正在加载环境信息…</template>
          </div>
        </div>
      </div>
      <div class="ws-actions">
        <el-tag size="small" :type="statusType">{{ statusText }}</el-tag>
        <el-button size="small" :disabled="state === 'connecting' || !!errorMsg" @click="reconnect">重新连接</el-button>
        <el-button size="small" text @click="goPlatform">返回平台</el-button>
      </div>
    </header>
    <el-alert v-if="errorMsg" type="error" :closable="false" show-icon :title="errorMsg" class="ws-alert" />
    <el-alert v-else-if="env && env.status !== 'running'" type="warning" :closable="false" show-icon
      :title="env.status_message || '环境尚未运行，启动后点击「重新连接」'" class="ws-alert" />
    <div v-show="!errorMsg" class="ws-term-wrap">
      <div ref="host" class="ws-term"></div>
    </div>
    <div v-if="!errorMsg" class="ws-tip">
      选中文本即可复制；粘贴请使用 Ctrl+Shift+V（macOS 为 Cmd+V）。断开后可点击「重新连接」。
    </div>
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import api from '@/api'

const route = useRoute()
const router = useRouter()
const envId = Number(route.params.id)
const env = ref(null)
const errorMsg = ref('')
const host = ref(null)
const state = ref('idle')
let term = null
let fit = null
let ws = null
let observer = null
let resizeTimer = null

const statusText = computed(() => ({
  idle: '未连接',
  connecting: '连接中',
  connected: '已连接',
  closed: '已断开',
  error: '连接失败'
}[state.value] || state.value))
const statusType = computed(() => ({
  connecting: 'warning',
  connected: 'success',
  error: 'danger'
}[state.value] || 'info'))

onMounted(async () => {
  if (!Number.isFinite(envId) || envId <= 0) {
    errorMsg.value = '无效的环境 ID'
    return
  }
  try {
    env.value = await api.get(`/environments/${envId}`)
    document.title = `${env.value.name} · WebSSH`
  } catch (e) {
    errorMsg.value = e.message || '无法访问该环境'
    return
  }
  await nextTick()
  initTerminal()
})

function initTerminal() {
  if (!host.value) return
  term = new Terminal({
    cursorBlink: true,
    convertEol: true,
    fontSize: 14,
    fontFamily: 'Menlo, Monaco, "Courier New", monospace',
    scrollback: 10000,
    theme: { background: '#0b1220', foreground: '#e2e8f0', cursor: '#38bdf8' }
  })
  fit = new FitAddon()
  term.loadAddon(fit)
  term.open(host.value)
  fit.fit()
  term.onData((data) => {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(data)
  })
  term.onResize(({ cols, rows }) => sendResize(cols, rows))
  observer = new ResizeObserver(() => {
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(() => fit && fit.fit(), 80)
  })
  observer.observe(host.value)
  term.focus()
  connect()
}

function connect() {
  if (!term) return
  closeSocket()
  const token = localStorage.getItem('rp_token') || ''
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  const url = `${proto}://${location.host}/api/environments/${envId}/webssh?token=${encodeURIComponent(token)}`
  state.value = 'connecting'
  ws = new WebSocket(url)
  ws.binaryType = 'arraybuffer'
  ws.onopen = () => {
    state.value = 'connected'
    fit && fit.fit()
    sendResize(term.cols, term.rows)
    term.focus()
  }
  ws.onmessage = (ev) => {
    if (typeof ev.data === 'string') {
      try {
        const msg = JSON.parse(ev.data)
        if (msg.type === 'error') {
          state.value = 'error'
          term.writeln(`\r\n\x1b[31m${msg.message}\x1b[0m`)
        } else if (msg.type === 'ready') {
          state.value = 'connected'
        }
        return
      } catch {
        term.write(ev.data)
        return
      }
    }
    if (ev.data instanceof ArrayBuffer) {
      term.write(new Uint8Array(ev.data))
    } else if (ev.data instanceof Blob) {
      ev.data.arrayBuffer().then((buf) => term && term.write(new Uint8Array(buf)))
    }
  }
  ws.onerror = () => {
    if (state.value !== 'error') {
      term.writeln('\r\n\x1b[31mWebSSH 连接失败，请确认环境正在运行。\x1b[0m')
    }
    state.value = 'error'
  }
  ws.onclose = () => {
    if (state.value !== 'error') {
      state.value = 'closed'
      term && term.writeln('\r\n\x1b[90mWebSSH 连接已断开\x1b[0m')
    }
  }
}

function sendResize(cols, rows) {
  if (ws && ws.readyState === WebSocket.OPEN && cols > 0 && rows > 0) {
    ws.send(JSON.stringify({ type: 'resize', cols, rows }))
  }
}

function closeSocket() {
  if (!ws) return
  ws.onopen = ws.onmessage = ws.onerror = ws.onclose = null
  try { ws.close() } catch {}
  ws = null
}

async function reconnect() {
  try {
    env.value = await api.get(`/environments/${envId}`)
  } catch (e) {
    errorMsg.value = e.message || '无法访问该环境'
    return
  }
  connect()
}

function goPlatform() {
  router.push('/environments')
}

function teardown() {
  closeSocket()
  if (observer) {
    observer.disconnect()
    observer = null
  }
  if (term) {
    term.dispose()
    term = null
  }
  fit = null
  state.value = 'idle'
}

onBeforeUnmount(teardown)
</script>

<style scoped>
.webssh-page {
  position: fixed; inset: 0; display: flex; flex-direction: column;
  background: #0b1220; color: #e2e8f0;
}
.ws-head {
  display: flex; align-items: center; justify-content: space-between; gap: 16px;
  padding: 12px 18px; background: #111c31; border-bottom: 1px solid #1e293b;
}
.ws-title { display: flex; align-items: center; gap: 10px; color: #38bdf8; }
.ws-name { color: #f8fafc; font-size: 15px; font-weight: 600; }
.ws-sub { color: #94a3b8; font-size: 12px; margin-top: 2px; }
.ws-actions { display: flex; align-items: center; gap: 10px; }
.ws-actions :deep(.el-button.is-text) { color: #cbd5e1; }
.ws-alert { margin: 12px 18px 0; }
.ws-term-wrap { flex: 1; min-height: 0; display: flex; padding: 10px 14px; }
.ws-term { flex: 1; min-height: 0; overflow: hidden; }
.ws-term :deep(.xterm) { height: 100%; }
.ws-tip {
  flex-shrink: 0; padding: 6px 18px 10px; color: #64748b; font-size: 12px;
  border-top: 1px solid #16213a; background: #0b1220;
}
</style>