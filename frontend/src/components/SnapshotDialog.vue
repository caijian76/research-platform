<template>
  <el-dialog :model-value="modelValue" title="环境快照" width="960px" destroy-on-close
    @update:model-value="$emit('update:modelValue', $event)" @closed="stopPoll">
    <div v-if="env">
      <div class="head">
        <div>
          <div style="font-size: 16px; font-weight: 700">{{ env.name }}</div>
          <div class="small muted">
            {{ env.unit_name }} · 命名空间 <span class="mono">{{ env.namespace }}</span> ·
            RootPV {{ gi(env.storage_gi) }} GiB
          </div>
        </div>
        <div class="head-actions">
          <el-button type="primary" :disabled="!canManage || atLimit" @click="openCreate">
            <el-icon><Camera /></el-icon>&nbsp;创建快照
          </el-button>
          <el-button circle :loading="loading" @click="load()"><el-icon><Refresh /></el-icon></el-button>
        </div>
      </div>

      <el-alert v-if="!canRestore" type="warning" :closable="false" show-icon style="margin-bottom: 12px"
        title="当前环境未停止：可以创建和删除快照；恢复快照需先停止环境。" />
      <el-alert v-else type="info" :closable="false" show-icon style="margin-bottom: 12px"
        title="快照保存在集群存储中。恢复快照会用快照内容覆盖当前根文件系统数据，且不可撤销。" />

      <el-table :data="snapshots" v-loading="loading" empty-text="还没有快照">
        <el-table-column label="快照名称" min-width="160">
          <template #default="{ row }">
            <div style="font-weight: 600">{{ row.name }}</div>
            <div class="small muted mono">{{ row.k8s_name }}</div>
          </template>
        </el-table-column>
        <el-table-column label="描述" min-width="180">
          <template #default="{ row }">{{ row.description || '-' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="120">
          <template #default="{ row }">
            <el-tooltip :content="row.error || '快照可用'" placement="top" :disabled="!row.error">
              <el-tag size="small" :type="snapshotStatus(row.status).type">{{ snapshotStatus(row.status).label }}</el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="100">
          <template #default="{ row }">{{ fmtSize(row.size_bytes) }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="180">
          <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" :disabled="!canRestore || row.status !== 'ready'"
              :title="canRestore ? '从该快照恢复' : '请先停止环境后再恢复快照'"
              @click="restore(row)">恢复</el-button>
            <el-button link type="danger" :disabled="!canManage" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="small muted" style="margin-top: 10px">
        已保存 {{ snapshots.length }}/5 个快照。每个环境最多同时保存 5 个快照，达到上限后请先删除旧快照。
        <template v-if="hasCreating">正在创建的快照会自动刷新状态，通常需要数秒到数分钟。</template>
      </div>
    </div>

    <el-dialog v-model="createOpen" title="创建快照" width="480px" append-to-body destroy-on-close>
      <el-form :model="createForm" label-width="90px">
        <el-form-item label="快照名称" required>
          <el-input v-model="createForm.name" maxlength="64" show-word-limit placeholder="例如 安装CUDA前" />
        </el-form-item>
        <el-form-item label="快照描述">
          <el-input v-model="createForm.description" type="textarea" :rows="3" maxlength="500" show-word-limit
            placeholder="可选，说明快照包含的内容" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createOpen = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="create">创建快照</el-button>
      </template>
    </el-dialog>
  </el-dialog>
</template>

<script setup>
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'
import { gi, fmtTime } from '@/utils'

const props = defineProps({ modelValue: Boolean, env: Object })
const emit = defineEmits(['update:modelValue', 'restored'])

const snapshots = ref([])
const loading = ref(false)
const createOpen = ref(false)
const creating = ref(false)
const createForm = reactive({ name: '', description: '' })
let timer = null

const SNAPSHOT_LIMIT = 5
const atLimit = computed(() => snapshots.value.length >= SNAPSHOT_LIMIT)
const hasCreating = computed(() => snapshots.value.some((s) => s.status === 'creating'))
const canManage = computed(() => !!props.env?.rootpv)
const canRestore = computed(() => canManage.value
  && props.env?.desired_state === 'stopped'
  && props.env?.status === 'stopped')

function snapshotStatus(s) {
  if (s === 'ready') return { label: '可用', type: 'success' }
  if (s === 'error') return { label: '失败', type: 'danger' }
  return { label: '创建中', type: 'warning' }
}

function fmtSize(bytes) {
  const n = Number(bytes) || 0
  if (n <= 0) return '-'
  if (n >= 1024 ** 3) return `${(n / 1024 ** 3).toFixed(2)} GiB`
  return `${(n / 1024 ** 2).toFixed(0)} MiB`
}

async function load(silent = false) {
  if (!props.env?.id) return
  if (!silent) loading.value = true
  try {
    snapshots.value = await api.get(`/environments/${props.env.id}/snapshots`)
  } finally {
    loading.value = false
  }
}

function startPoll() {
  stopPoll()
  timer = setInterval(() => {
    if (snapshots.value.some((s) => s.status === 'creating')) load(true).catch(() => {})
  }, 4000)
}

function stopPoll() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

function applyOpen(open) {
  if (!open) {
    stopPoll()
    createOpen.value = false
    return
  }
  stopPoll()
  snapshots.value = []
  load().catch(() => {})
  startPoll()
}

function openCreate() {
  if (atLimit.value) {
    ElMessage.warning('每个环境最多同时保存 5 个快照，请先删除旧快照')
    return
  }
  createForm.name = ''
  createForm.description = ''
  createOpen.value = true
}

async function create() {
  if (snapshots.value.length >= SNAPSHOT_LIMIT) {
    ElMessage.warning('每个环境最多同时保存 5 个快照，请先删除旧快照')
    return
  }
  const name = createForm.name.trim()
  if (!name) {
    ElMessage.warning('请填写快照名称')
    return
  }
  creating.value = true
  try {
    await api.post(`/environments/${props.env.id}/snapshots`, {
      name,
      description: createForm.description.trim()
    })
    ElMessage.success('快照创建任务已提交')
    createOpen.value = false
    await load(true)
  } finally {
    creating.value = false
  }
}

async function restore(row) {
  try {
    await ElMessageBox.confirm(
      `将使用快照「${row.name}」覆盖环境当前的根文件系统数据，此操作不可撤销。恢复完成后环境仍保持关机状态。`,
      '恢复到此快照',
      { type: 'warning', confirmButtonText: '恢复', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch {
    return
  }
  loading.value = true
  try {
    const res = await api.post(`/environments/${props.env.id}/snapshots/${row.id}/restore`, {}, { timeout: 180000 })
    ElMessage.success(res.message || '快照恢复完成')
    emit('restored')
    await load(true)
  } finally {
    loading.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除快照「${row.name}」吗？删除后不可恢复。`, '删除快照',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' })
  } catch {
    return
  }
  await api.delete(`/environments/${props.env.id}/snapshots/${row.id}`)
  ElMessage.success('快照已删除')
  await load(true)
}

watch(() => [props.modelValue, props.env?.id], ([open]) => applyOpen(open))
onBeforeUnmount(stopPoll)
</script>

<style scoped>
.head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 14px; }
.head-actions { display: flex; align-items: center; gap: 8px; }
</style>