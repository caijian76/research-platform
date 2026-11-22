<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">容器模板管理</h2>
        <p class="page-sub">定义科研环境使用的镜像、默认规格、根文件系统模式（持久化/临时）与 SSH / noVNC 端口</p>
      </div>
      <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon>&nbsp;新建模板</el-button>
    </div>

    <div class="panel">
      <el-table :data="templates" v-loading="loading">
        <el-table-column label="模板" min-width="220">
          <template #default="{ row }">
            <div style="font-weight: 600">{{ row.name }}</div>
            <div class="small muted">{{ row.description || '-' }}</div>
          </template>
        </el-table-column>
        <el-table-column label="镜像" min-width="220">
          <template #default="{ row }"><span class="mono">{{ row.image }}</span></template>
        </el-table-column>
        <el-table-column label="支持模式" width="150">
          <template #default="{ row }">
            <el-tag v-if="row.rootpv" size="small" type="success">持久化</el-tag>
            <el-tag v-if="row.ephemeral" size="small" type="info">临时</el-tag>
            <span v-if="!row.rootpv && !row.ephemeral" class="muted">未配置</span>
          </template>
        </el-table-column>
        <el-table-column label="默认规格" width="190">
          <template #default="{ row }">
            {{ cpuCores(row.cpu_milli) }} 核 / {{ memGiB(row.mem_mi) }} GiB / {{ gi(row.storage_gi) }} GiB
          </template>
        </el-table-column>
        <el-table-column label="端口" width="120">
          <template #default="{ row }">22→{{ row.ssh_port }} / {{ row.vnc_port }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!loading && !templates.length" description="暂无模板" />
    </div>

    <el-dialog v-model="dialog" :title="editing ? '编辑模板' : '新建模板'" width="620px" destroy-on-close>
      <el-form :model="form" label-width="140px">
        <el-form-item label="模板名称" required>
          <el-input v-model="form.name" maxlength="60" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" maxlength="200" />
        </el-form-item>
        <el-form-item label="容器镜像" required>
          <el-input v-model="form.image" placeholder="例如 research-workspace:22.04" />
          <div class="small muted" style="margin-top: 4px">镜像需内置 SSHD 与 noVNC/websockify 服务</div>
        </el-form-item>
        <el-form-item label="根文件系统" required>
          <el-checkbox-group v-model="form.modes" @change="onModeChange">
            <el-checkbox value="persistent">持久化</el-checkbox>
            <el-checkbox value="ephemeral">临时</el-checkbox>
          </el-checkbox-group>
          <div class="small muted" style="width: 100%; margin-top: 4px">
            可单选或多选；用户创建环境时只能从模板支持的模式中选择，创建后不可修改
          </div>
        </el-form-item>
        <el-form-item label="默认 CPU" required>
          <el-input-number v-model="form.cpu_cores" :min="0.1" :step="0.5" :precision="2" /><span class="unit">核</span>
        </el-form-item>
        <el-form-item label="默认内存" required>
          <el-input-number v-model="form.mem_gib" :min="0.25" :step="1" :precision="1" /><span class="unit">GiB</span>
        </el-form-item>
        <el-form-item label="默认存储" :required="hasPersistent">
          <el-input-number v-model="form.storage_gib" :min="0" :step="5" :precision="0"
            :disabled="!hasPersistent" /><span class="unit">GiB</span>
          <div class="small muted" style="width: 100%; margin-top: 4px">
            仅持久化模式使用根目录 PVC；临时模式不占用存储配额
          </div>
        </el-form-item>
        <el-form-item label="SSH 容器端口" required>
          <el-input-number v-model="form.ssh_port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="noVNC 容器端口" required>
          <el-input-number v-model="form.vnc_port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="状态">
          <el-switch v-model="form.enabled" active-text="启用" inactive-text="停用" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'
import { cpuCores, memGiB, gi } from '@/utils'

const templates = ref([])
const loading = ref(false)
const dialog = ref(false)
const editing = ref(null)
const saving = ref(false)
const form = reactive({
  name: '', description: '', image: 'research-workspace:22.04', modes: ['persistent'],
  cpu_cores: 2, mem_gib: 4, storage_gib: 10, ssh_port: 22, vnc_port: 6901, enabled: true
})
const hasPersistent = computed(() => form.modes.includes('persistent'))

async function load() {
  loading.value = true
  try {
    templates.value = await api.get('/admin/templates')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  Object.assign(form, {
    name: '', description: '', image: 'research-workspace:22.04', modes: ['persistent'],
    cpu_cores: 2, mem_gib: 4, storage_gib: 10, ssh_port: 22, vnc_port: 6901, enabled: true
  })
  dialog.value = true
}

function onModeChange() {
  if (!hasPersistent.value) {
    form.storage_gib = 0
  } else if (!Number(form.storage_gib)) {
    form.storage_gib = 10
  }
}

function openEdit(row) {
  editing.value = row
  const modes = []
  if (row.rootpv) modes.push('persistent')
  if (row.ephemeral) modes.push('ephemeral')
  Object.assign(form, {
    name: row.name,
    description: row.description,
    image: row.image,
    modes,
    cpu_cores: Number(row.cpu_milli) / 1000,
    mem_gib: Number(row.mem_mi) / 1024,
    storage_gib: row.rootpv ? Number(row.storage_gi) : 0,
    ssh_port: row.ssh_port,
    vnc_port: row.vnc_port,
    enabled: row.enabled
  })
  dialog.value = true
}

async function save() {
  if (!form.modes.length) {
    ElMessage.warning('请至少选择一种根文件系统模式（持久化或临时）')
    return
  }
  if (hasPersistent.value && Number(form.storage_gib) <= 0) {
    ElMessage.warning('支持持久化的模板必须配置大于 0 的默认存储')
    return
  }
  const payload = {
    name: form.name,
    description: form.description,
    image: form.image,
    rootpv: form.modes.includes('persistent'),
    ephemeral: form.modes.includes('ephemeral'),
    cpu_milli: Math.round(Number(form.cpu_cores) * 1000),
    mem_mi: Math.round(Number(form.mem_gib) * 1024),
    storage_gi: hasPersistent.value ? Math.round(Number(form.storage_gib)) : 0,
    ssh_port: Number(form.ssh_port),
    vnc_port: Number(form.vnc_port),
    enabled: form.enabled
  }
  if (!payload.name || !payload.image) {
    ElMessage.warning('请填写模板名称和镜像')
    return
  }
  saving.value = true
  try {
    if (editing.value) {
      await api.patch(`/admin/templates/${editing.value.id}`, payload)
      ElMessage.success('模板已更新')
    } else {
      await api.post('/admin/templates', payload)
      ElMessage.success('模板已创建')
    }
    dialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(`确定删除模板「${row.name}」吗？已创建的环境不受影响。`, '删除模板',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
    await api.delete(`/admin/templates/${row.id}`)
    ElMessage.success('模板已删除')
    await load()
  } catch {}
}

onMounted(load)
</script>

<style scoped>
.unit { margin-left: 8px; color: #6b7280; font-size: 13px; }
</style>