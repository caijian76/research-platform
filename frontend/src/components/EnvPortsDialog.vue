<template>
  <el-dialog :model-value="modelValue" title="端口映射" width="820px" destroy-on-close
    @update:model-value="$emit('update:modelValue', $event)">
    <div v-if="env" v-loading="loading">
      <div class="head">
        <div>
          <div style="font-size: 16px; font-weight: 700">{{ env.name }}</div>
          <div class="small muted">
            {{ env.unit_name }} · 命名空间 <span class="mono">{{ env.namespace }}</span>
          </div>
        </div>
        <div class="head-actions">
          <el-button type="primary" :disabled="!data.can_add" :title="!data.can_add ? `最多添加 ${data.max} 个自定义端口` : ''"
            @click="openAdd">
            <el-icon><Plus /></el-icon>&nbsp;添加端口
          </el-button>
          <el-button circle :loading="loading" @click="load"><el-icon><Refresh /></el-icon></el-button>
        </div>
      </div>

      <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px"
        title="自定义端口映射把容器内端口通过 NodePort 暴露到集群外部；NodePort 由 Kubernetes 自动分配，无法指定。请在容器内启动对应服务后使用。" />

      <el-table :data="data.items" v-loading="loading" empty-text="还没有端口映射">
        <el-table-column label="类型" width="150">
          <template #default="{ row }">
            <el-tag size="small" :type="kindTag(row.kind).type" effect="plain">{{ kindTag(row.kind).label }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="容器内端口" width="130">
          <template #default="{ row }">
            <span class="mono">{{ row.container_port }}</span>
            <span class="small muted"> / {{ row.protocol || 'TCP' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="NodePort" width="120">
          <template #default="{ row }">
            <span class="mono">{{ row.node_port || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="访问地址" min-width="200">
          <template #default="{ row }">
            <div v-if="row.address" class="addr">
              <span class="mono">{{ row.address }}</span>
              <el-button link type="primary" size="small" title="复制地址" @click="copy(row.address)">
                <el-icon><CopyDocument /></el-icon>
              </el-button>
            </div>
            <span v-else class="small muted">环境启动后分配</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <el-tooltip v-if="row.builtin" content="SSH / VNC 端口由平台管理，不能删除" placement="top">
              <span class="small muted">系统端口</span>
            </el-tooltip>
            <el-button v-else link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="small muted" style="margin-top: 10px">
        自定义端口 {{ data.count || 0 }}/{{ data.max }} 个（SSH 与图形桌面端口不计入）。达到上限后请先删除已有自定义端口。
      </div>
    </div>

    <el-dialog v-model="addOpen" title="添加端口映射" width="460px" append-to-body destroy-on-close>
      <el-form :model="form" label-width="110px">
        <el-form-item label="容器内端口" required>
          <el-input-number v-model="form.container_port" :min="1" :max="65535" :step="1" :precision="0" style="width: 100%" />
          <div class="small muted" style="width: 100%; margin-top: 4px">
            填写容器内服务监听的端口，例如 Jupyter 8888、TensorBoard 6006
          </div>
        </el-form-item>
        <el-form-item label="协议">
          <el-select v-model="form.protocol" style="width: 100%">
            <el-option label="TCP" value="TCP" />
            <el-option label="UDP" value="UDP" />
          </el-select>
        </el-form-item>
        <el-form-item label="NodePort">
          <el-input value="由 Kubernetes 自动分配" disabled />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="addOpen = false">取消</el-button>
        <el-button type="primary" :loading="adding" @click="create">添加</el-button>
      </template>
    </el-dialog>
  </el-dialog>
</template>

<script setup>
import { reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'
import { copyText } from '@/utils'

const props = defineProps({ modelValue: Boolean, envId: Number, env: Object })
const emit = defineEmits(['update:modelValue', 'changed'])

const data = ref({ items: [], max: 5, host: '', count: 0, can_add: true })
const loading = ref(false)
const addOpen = ref(false)
const adding = ref(false)
const form = reactive({ container_port: 8888, protocol: 'TCP' })

function kindTag(kind) {
  if (kind === 'ssh') return { label: 'SSH', type: 'info' }
  if (kind === 'vnc') return { label: '图形桌面', type: 'success' }
  return { label: '自定义', type: 'primary' }
}

async function load() {
  if (!props.envId) return
  loading.value = true
  try {
    data.value = await api.get(`/environments/${props.envId}/ports`)
  } finally {
    loading.value = false
  }
}

function openAdd() {
  if (!data.value.can_add) {
    ElMessage.warning(`每个环境最多添加 ${data.value.max} 个自定义端口`)
    return
  }
  form.container_port = 8888
  form.protocol = 'TCP'
  addOpen.value = true
}

async function create() {
  const port = Number(form.container_port)
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    ElMessage.warning('容器端口必须是 1 到 65535 之间的整数')
    return
  }
  adding.value = true
  try {
    await api.post(`/environments/${props.envId}/ports`, {
      container_port: port,
      protocol: form.protocol
    })
    ElMessage.success('端口映射已添加，NodePort 由系统自动分配')
    addOpen.value = false
    await load()
    emit('changed')
  } finally {
    adding.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(
      `确定删除容器端口 ${row.container_port}（NodePort ${row.node_port}）的映射吗？删除后该端口将无法从外部访问。`,
      '删除端口映射',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch {
    return
  }
  await api.delete(`/environments/${props.envId}/ports/${row.id}`)
  ElMessage.success('端口映射已删除')
  await load()
  emit('changed')
}

async function copy(text) {
  if (!text) return
  if (await copyText(text)) ElMessage.success('已复制')
}

watch(
  () => [props.modelValue, props.envId],
  ([open]) => {
    if (open) {
      addOpen.value = false
      load().catch(() => {})
    }
  }
)
</script>

<style scoped>
.head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; margin-bottom: 14px; }
.head-actions { display: flex; align-items: center; gap: 8px; }
.addr { display: flex; align-items: center; gap: 4px; }
</style>
