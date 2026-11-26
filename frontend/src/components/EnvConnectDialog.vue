<template>
  <el-dialog :model-value="modelValue" title="连接科研环境" width="720px" destroy-on-close
    @update:model-value="$emit('update:modelValue', $event)">
    <div v-if="env" v-loading="loading">
      <div class="head">
        <div>
          <div style="font-size: 16px; font-weight: 700">{{ env.name }}</div>
          <div class="small muted">
            {{ env.unit_name }} · 命名空间 <span class="mono">{{ env.namespace }}</span>
          </div>
        </div>
        <div>
          <el-tag :type="statusOf(env.status).type">{{ statusOf(env.status).label }}</el-tag>
          <el-button size="small" circle style="margin-left: 8px" @click="load"><el-icon><Refresh /></el-icon></el-button>
        </div>
      </div>
      <el-alert v-if="env.status !== 'running'" type="warning" :closable="false" show-icon style="margin-bottom: 14px"
        :title="env.status_message || '环境尚未运行，服务可用后即可连接'" />

      <div class="conn-grid" :class="{ single: !env.graphical }">
        <div class="panel inner">
          <div class="conn-head">
            <h4><el-icon><Document /></el-icon> 文本界面 (SSH)</h4>
            <el-tag size="small" type="info" effect="plain">命令行</el-tag>
          </div>
          <p class="small muted">支持任意 SSH 客户端，也可在新标签页打开浏览器终端</p>
          <div class="conn-actions">
            <el-button type="primary" :disabled="env.status !== 'running'" @click="openWebSSH">
              <el-icon><TopRight /></el-icon>&nbsp;WebSSH 终端
            </el-button>
          </div>
          <div class="code-box">
            {{ env.ssh?.command || `ssh -p ${env.node_port_ssh} ${env.ssh_user}@${env.ssh?.host}` }}
            <el-button class="copy" size="small" text title="复制 SSH 命令" @click="copy(env.ssh?.command)">
              <el-icon><CopyDocument /></el-icon>
            </el-button>
          </div>
          <div class="kv">
            <span class="muted">主机</span><span class="mono">{{ env.ssh?.host }}</span>
            <span class="muted">端口</span><span class="mono">{{ env.node_port_ssh || '-' }}</span>
            <span class="muted">用户</span><span class="mono">{{ env.ssh_user }}</span>
            <span class="muted">密码</span>
            <span>
              <span class="mono">{{ showSsh ? env.ssh?.password : '••••••••' }}</span>
              <el-button link type="primary" size="small" @click="showSsh = !showSsh">{{ showSsh ? '隐藏' : '显示' }}</el-button>
              <el-button link type="primary" size="small" @click="copy(env.ssh?.password)">复制</el-button>
            </span>
          </div>
        </div>

        <div v-if="env.graphical" class="panel inner">
          <div class="conn-head">
            <h4><el-icon><Monitor /></el-icon> 图形桌面 (noVNC)</h4>
            <el-tag size="small" type="success" effect="plain">图形界面</el-tag>
          </div>
          <p class="small muted">在浏览器中打开图形桌面，或复制下方的直连地址</p>
          <div class="conn-actions">
            <el-button type="primary" :disabled="env.status !== 'running' || !env.node_port_vnc" @click="openVNC">
              <el-icon><TopRight /></el-icon>&nbsp;打开 noVNC 桌面
            </el-button>
          </div>
          <div class="code-box">
            {{ vncDisplayUrl }}
            <el-button class="copy" size="small" text title="复制含密码的完整地址" @click="copy(env.vnc?.url)">
              <el-icon><CopyDocument /></el-icon>
            </el-button>
          </div>
          <div class="kv">
            <span class="muted">主机</span><span class="mono">{{ env.vnc?.host || env.ssh?.host }}</span>
            <span class="muted">端口</span><span class="mono">{{ env.node_port_vnc || '-' }}</span>
            <span class="muted">密码</span>
            <span>
              <span class="mono">{{ showVnc ? env.vnc?.password : '••••••••' }}</span>
              <el-button link type="primary" size="small" @click="showVnc = !showVnc">{{ showVnc ? '隐藏' : '显示' }}</el-button>
              <el-button link type="primary" size="small" @click="copy(env.vnc?.password)">复制</el-button>
            </span>
          </div>
        </div>
      </div>

      <div class="meta">
        <span>环境 ID：{{ env.id }}</span>
        <span>RootPV：{{ env.rootpv ? '持久化根文件系统' : '临时' }}</span>
        <span>规格：{{ cpuCores(env.cpu_milli) }} 核 / {{ memGiB(env.mem_mi) }} GiB / {{ gi(env.storage_gi) }} GiB</span>
        <span>创建于 {{ fmtTime(env.created_at) }}</span>
      </div>
    </div>
  </el-dialog>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { copyText, cpuCores, memGiB, gi, statusOf, fmtTime } from '@/utils'

const props = defineProps({ modelValue: Boolean, envId: Number })
const emit = defineEmits(['update:modelValue'])

const env = ref(null)
const loading = ref(false)
const showSsh = ref(false)
const showVnc = ref(false)

const vncDisplayUrl = computed(() => {
  const url = env.value?.vnc?.url
  if (!url) return '-'
  try {
    const u = new URL(url)
    u.search = ''
    return u.toString()
  } catch {
    return url.split('?')[0]
  }
})

async function load() {
  if (!props.envId) return
  loading.value = true
  try {
    env.value = await api.get(`/environments/${props.envId}`)
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.modelValue, props.envId],
  ([open]) => {
    if (open) {
      showSsh.value = false
      showVnc.value = false
      load()
    }
  }
)

async function copy(text) {
  if (!text) return
  if (await copyText(text)) ElMessage.success('已复制')
}

function closeDialog() {
  emit('update:modelValue', false)
}

function openVNC() {
  const url = env.value?.vnc?.url
  if (!url) return
  window.open(url, '_blank')
  closeDialog()
}

function openWebSSH() {
  if (!env.value?.id) return
  window.open(`/webssh/${env.value.id}`, '_blank', 'noopener')
  closeDialog()
}

defineExpose({ reload: load })
</script>

<style scoped>
.head { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 14px; }
.conn-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; align-items: stretch; }
.conn-grid.single { grid-template-columns: 1fr; }
.inner { margin-bottom: 0; padding: 18px; display: flex; flex-direction: column; }
.conn-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.inner h4 { margin: 0; display: flex; align-items: center; gap: 6px; font-size: 14px; font-weight: 600; }
.inner > p { margin: 8px 0 0; line-height: 1.5; min-height: 36px; }
.conn-actions { margin: 10px 0; }
.conn-actions .el-button { width: 100%; margin-left: 0; }
.code-box { margin-bottom: 0; }
.code-box .copy { color: #94a3b8; }
.kv { display: grid; grid-template-columns: 44px 1fr; row-gap: 8px; font-size: 13px; margin-top: 12px; align-items: center; }
.meta { display: flex; gap: 18px; flex-wrap: wrap; color: #6b7280; font-size: 12px; margin-top: 16px; border-top: 1px solid #f1f5f9; padding-top: 12px; }
@media (max-width: 700px) { .conn-grid { grid-template-columns: 1fr; } }
</style>