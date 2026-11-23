<template>
  <div>
    <el-table :data="envs" v-loading="loading">
      <el-table-column label="环境" min-width="190">
        <template #default="{ row }">
          <div style="font-weight: 600">{{ row.name }}</div>
          <div class="small muted">{{ row.template_name || row.image }}</div>
        </template>
      </el-table-column>
      <el-table-column label="单位 / 用户" min-width="150">
        <template #default="{ row }">
          <div>{{ row.unit_name }}</div>
          <div class="small muted mono">{{ row.username }}</div>
        </template>
      </el-table-column>
      <el-table-column label="规格" width="170">
        <template #default="{ row }">
          {{ cpuCores(row.cpu_milli) }} 核 / {{ memGiB(row.mem_mi) }} GiB
          <div class="small muted">{{ row.rootpv ? `RootPV ${gi(row.storage_gi)} GiB` : '临时根目录' }}</div>
          <div v-if="row.shm_enabled" class="small muted">大SHM {{ row.shm_mi }} MiB</div>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="120">
        <template #default="{ row }">
          <el-tooltip :content="row.status_message || row.status" placement="top">
            <el-tag size="small" :type="statusOf(row.status).type">{{ statusOf(row.status).label }}</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="连接端口" width="140">
        <template #default="{ row }">
          <div class="mono small">SSH {{ row.node_port_ssh || '-' }}</div>
          <div class="mono small">VNC {{ row.graphical ? (row.node_port_vnc || '-') : '未开启' }}</div>
        </template>
      </el-table-column>
      <el-table-column label="创建时间" width="170">
        <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="400" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openConnect(row)">连接</el-button>
          <el-button v-if="row.status === 'stopped'" link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button v-if="row.rootpv" link type="primary" title="管理环境快照"
            @click="openSnapshots(row)">快照</el-button>
          <el-button v-if="row.desired_state === 'running' && row.status !== 'stopped'" link @click="act(row, 'stop', '已停止，数据保留')">停止</el-button>
          <el-button v-if="row.desired_state !== 'running' || row.status === 'stopped'" link type="success" @click="act(row, 'start', '正在启动')">启动</el-button>
          <el-button v-if="row.status === 'running'" link @click="act(row, 'restart', '正在重启')">重启</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && !envs.length" :description="emptyText" />

    <el-dialog v-model="editDialog" title="编辑科研环境" width="580px" destroy-on-close>
      <el-form v-if="editingRow" :model="form" label-width="100px">
        <div v-if="ownerQuota" class="quota-hint">
          所属用户 {{ editingRow.username }} 的资源配额：CPU {{ cpuCores(ownerQuota.cpu) }} 核 · 内存 {{ memGiB(ownerQuota.mem) }} GiB · 存储 {{ gi(ownerQuota.storage) }} GiB
          <span class="muted">（运行中已用 CPU {{ cpuCores(ownerQuota.used_cpu) }} 核 / 内存 {{ memGiB(ownerQuota.used_mem) }} GiB；全部环境已分配存储 {{ gi(ownerQuota.used_storage) }} GiB）</span>
          <div class="small muted" style="margin-top: 3px">
            单个环境的 CPU/内存不能超过配额，全部环境存储合计不能超过配额；存储只能增加，启动时再校验运行资源。
          </div>
        </div>
        <el-form-item label="容器模板">
          <el-tag size="small" type="info">{{ editingRow.template_name || '-' }}</el-tag>
          <span class="small muted" style="margin-left: 8px">创建后不可修改</span>
        </el-form-item>
        <el-form-item label="环境模式">
          <el-tag size="small" :type="editingRow.rootpv ? 'success' : 'info'">{{ editingRow.rootpv ? '持久化' : '临时' }}</el-tag>
          <span class="small muted" style="margin-left: 8px">创建后不可修改</span>
        </el-form-item>
        <el-form-item label="环境名称">
          <el-input v-model="form.name" maxlength="64" placeholder="可选，例如 分子动力学实验" />
        </el-form-item>
        <el-form-item label="root 密码">
          <el-input v-model="form.root_password" type="password" show-password maxlength="64"
            placeholder="留空保持原密码" />
          <div class="small muted" style="width: 100%; margin-top: 4px">
            留空表示不修改；新密码在下次启动后生效
          </div>
        </el-form-item>
        <el-form-item label="图形化界面">
          <el-switch v-model="form.graphical" />
          <div class="small muted" style="width: 100%; margin-top: 4px">
            {{ form.graphical ? '容器启动 vnc + websockify，可通过 noVNC 桌面连接' : '仅启动 sshd，不提供图形桌面' }}
          </div>
        </el-form-item>
        <el-form-item v-if="form.graphical" label="VNC 密码">
          <el-input v-model="form.vnc_password" type="password" show-password maxlength="8"
            placeholder="留空保持原密码" />
          <div class="small muted" style="width: 100%; margin-top: 4px">
            留空表示不修改；如原本未开启图形界面，开启时将自动生成密码
          </div>
        </el-form-item>
        <el-form-item label="CPU" required>
          <el-input-number v-model="form.cpu_cores" :min="0.1" :step="0.5" :precision="2" /><span class="unit">核</span>
        </el-form-item>
        <el-form-item label="内存" required>
          <div class="mem-line">
            <el-input-number v-model="form.mem_gib" :min="0.25" :step="1" :precision="1" /><span class="unit">GiB</span>
            <div class="shm-block">
              <div class="shm-switch">
                <el-tooltip content="写入SHM的内容，会占用内存，请合理分配大小，避免OOM" placement="top">
                  <el-icon class="shm-help"><QuestionFilled /></el-icon>
                </el-tooltip>
                <el-switch v-model="form.shm_enabled" active-text="大shm" @change="onShmToggle" />
              </div>
              <template v-if="form.shm_enabled">
                <el-input-number v-model="form.shm_mi" :min="1" :max="shmMaxMi" :step="256" :precision="0" /><span class="unit">Mi</span>
              </template>
            </div>
          </div>
        </el-form-item>
        <el-form-item v-if="form.rootpv" label="存储" required>
          <el-input-number v-model="form.storage_gib" :min="Number(editingRow.storage_gi)" :step="5" :precision="0" /><span class="unit">GiB</span>
          <div class="small muted" style="width: 100%; margin-top: 4px">
            存储只能增加，不能少于当前 {{ gi(editingRow.storage_gi) }} GiB；增加后后台会自动扩容 PVC
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveEdit">保存修改</el-button>
      </template>
    </el-dialog>

    <EnvConnectDialog v-model="connectDialog" :env-id="connectEnvId" />
    <SnapshotDialog v-model="snapshotDialog" :env="snapshotEnv" @restored="emit('reload')" />
  </div>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'
import { useAuth } from '@/stores/auth'
import { cpuCores, memGiB, gi, statusOf, fmtTime } from '@/utils'
import EnvConnectDialog from './EnvConnectDialog.vue'
import SnapshotDialog from './SnapshotDialog.vue'

defineProps({
  envs: { type: Array, default: () => [] },
  loading: Boolean,
  emptyText: { type: String, default: '还没有科研环境' }
})
const emit = defineEmits(['reload'])

const auth = useAuth()
const editDialog = ref(false)
const editingRow = ref(null)
const ownerQuota = ref(null)
const saving = ref(false)
const connectDialog = ref(false)
const connectEnvId = ref(0)
const snapshotDialog = ref(false)
const snapshotEnv = ref(null)

const form = reactive({
  name: '', cpu_cores: 2, mem_gib: 4, storage_gib: 10,
  root_password: '', graphical: true, vnc_password: '', shm_enabled: false, shm_mi: 1024, rootpv: true
})

const shmMaxMi = computed(() => Math.max(1, Math.floor(Number(form.mem_gib || 0) * 512)))
watch(() => form.mem_gib, () => {
  if (form.shm_enabled && form.shm_mi > shmMaxMi.value) form.shm_mi = shmMaxMi.value
})

function openConnect(row) {
  connectEnvId.value = row.id
  connectDialog.value = true
}

function openSnapshots(row) {
  snapshotEnv.value = row
  snapshotDialog.value = true
}

async function loadOwnerQuota(row) {
  ownerQuota.value = null
  try {
    if (row.user_id === auth.user?.id) {
      const me = await auth.fetchMe()
      const m = (me.memberships || []).find((x) => x.unit_id === row.unit_id)
      if (m) {
        ownerQuota.value = {
          cpu: m.cpu_milli, mem: m.mem_mi, storage: m.storage_gi,
          used_cpu: m.used_cpu_milli || 0, used_mem: m.used_mem_mi || 0, used_storage: m.used_storage_gi || 0
        }
      }
      return
    }
    const members = await api.get(`/units/${row.unit_id}/members`)
    const m = members.find((x) => x.user_id === row.user_id)
    if (m) {
      ownerQuota.value = {
        cpu: m.cpu_milli, mem: m.mem_mi, storage: m.storage_gi,
        used_cpu: m.used_cpu_milli || 0, used_mem: m.used_mem_mi || 0, used_storage: m.used_storage_gi || 0
      }
    }
  } catch {}
}

async function openEdit(row) {
  if (row.status !== 'stopped') {
    ElMessage.warning('只能在环境停止后编辑')
    return
  }
  await loadOwnerQuota(row)
  editingRow.value = row
  Object.assign(form, {
    name: row.name,
    cpu_cores: Number(row.cpu_milli) / 1000,
    mem_gib: Number(row.mem_mi) / 1024,
    storage_gib: Number(row.storage_gi || 0),
    root_password: '',
    graphical: !!row.graphical,
    vnc_password: '',
    shm_enabled: !!row.shm_enabled,
    shm_mi: Number(row.shm_mi) || 1024,
    rootpv: !!row.rootpv
  })
  editDialog.value = true
}

function onShmToggle(enabled) {
  if (enabled && (!form.shm_mi || form.shm_mi > shmMaxMi.value)) {
    form.shm_mi = Math.min(1024, shmMaxMi.value)
  }
}

async function saveEdit() {
  const row = editingRow.value
  if (!row) return
  if (form.root_password && (form.root_password.length < 6 || form.root_password.length > 64)) {
    ElMessage.warning('root 密码长度需为 6-64 位')
    return
  }
  if (form.graphical && form.vnc_password && (form.vnc_password.length < 6 || form.vnc_password.length > 8)) {
    ElMessage.warning('VNC 密码长度需为 6-8 位')
    return
  }
  const oldStorage = Number(row.storage_gi || 0)
  const cpu = Math.round(Number(form.cpu_cores) * 1000)
  const mem = Math.round(Number(form.mem_gib) * 1024)
  const shmMi = form.shm_enabled ? Math.round(Number(form.shm_mi)) : 0
  if (form.shm_enabled) {
    if (shmMi <= 0) {
      ElMessage.warning('大 SHM 大小必须大于 0 MiB')
      return
    }
    if (shmMi * 2 > mem) {
      ElMessage.warning('大 SHM 大小不能超过内存的二分之一')
      return
    }
  }
  const storage = row.rootpv ? Math.round(Number(form.storage_gib)) : 0
  if (row.rootpv && storage <= 0) {
    ElMessage.warning('持久化模式必须配置大于 0 的存储容量')
    return
  }
  if (storage < oldStorage) {
    ElMessage.warning(`存储只能增加，不能少于当前的 ${gi(oldStorage)} GiB`)
    return
  }
  const q = ownerQuota.value
  if (q) {
    if (cpu > q.cpu || mem > q.mem) {
      ElMessage.warning('单个环境的 CPU / 内存不能超过个人资源上限')
      return
    }
    const otherStorage = Math.max(0, (q.used_storage || 0) - oldStorage)
    if (storage > q.storage - otherStorage) {
      ElMessage.warning('全部环境的存储总量将超出个人上限')
      return
    }
  }
  saving.value = true
  try {
    await api.patch(`/environments/${row.id}`, {
      name: form.name,
      cpu_milli: cpu,
      mem_mi: mem,
      storage_gi: storage,
      root_password: form.root_password || undefined,
      graphical: form.graphical,
      vnc_password: form.graphical ? form.vnc_password || undefined : undefined,
      shm_enabled: !!form.shm_enabled,
      shm_mi: shmMi
    })
    editDialog.value = false
    ElMessage.success('环境配置已更新，启动后生效')
    emit('reload')
  } finally {
    saving.value = false
  }
}

async function act(row, action, success) {
  try {
    await api.post(`/environments/${row.id}/${action}`)
    ElMessage.success(success)
    emit('reload')
  } catch {}
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(
      `确定删除环境「${row.name}」吗？${row.rootpv ? '其持久化根文件系统（PVC）也会被删除，数据不可恢复。' : ''}`,
      '删除环境', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
    await api.delete(`/environments/${row.id}`)
    ElMessage.success('环境已删除')
    emit('reload')
  } catch {}
}
</script>

<style scoped>
.quota-hint {
  background: #f8fafc; border: 1px dashed #dbe3ee; border-radius: 8px;
  padding: 8px 12px; font-size: 12px; margin: 0 0 14px; color: #374151;
}
.unit { margin-left: 8px; color: #6b7280; font-size: 13px; }
.mem-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; width: 100%; }
.mem-line :deep(.el-input-number) { width: 110px; }
.shm-block { display: inline-flex; align-items: center; gap: 6px; margin-left: 8px; }
.shm-block :deep(.el-input-number) { width: 140px; }
.shm-switch { position: relative; display: inline-flex; align-items: center; }
.shm-help {
  position: absolute; top: -14px; right: -2px; font-size: 14px; color: #6b7280; cursor: help;
}
</style>