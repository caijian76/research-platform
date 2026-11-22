<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">二级单位管理</h2>
        <p class="page-sub">创建二级单位并分配资源上限；单位内人员与环境由二级管理员管理</p>
      </div>
      <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon>&nbsp;新建单位</el-button>
    </div>

    <el-alert type="info" :closable="false" show-icon style="margin-bottom: 16px">
      <template #title>
        平台可分配：CPU {{ cpuCores(limits.cpu_milli) }} 核 · 内存 {{ memGiB(limits.mem_mi) }} GiB · 存储 {{ gi(limits.storage_gi) }} GiB，
        已分配：CPU {{ cpuCores(allocated.cpu_milli) }} 核 · 内存 {{ memGiB(allocated.mem_mi) }} GiB · 存储 {{ gi(allocated.storage_gi) }} GiB
      </template>
    </el-alert>

    <div class="panel">
      <el-table :data="units" v-loading="loading" size="default">
        <el-table-column label="单位" min-width="180">
          <template #default="{ row }">
            <div style="font-weight: 600">{{ row.name }}</div>
            <div class="small muted mono">{{ row.namespace }}</div>
          </template>
        </el-table-column>
        <el-table-column label="CPU (运行中/上限)" width="170">
          <template #default="{ row }">
            <div>{{ cpuCores(row.used_cpu_milli) }} / {{ cpuCores(row.cpu_milli) }} 核</div>
            <el-progress :percentage="pct(row.used_cpu_milli, row.cpu_milli)" :stroke-width="4" />
          </template>
        </el-table-column>
        <el-table-column label="内存 (运行中/上限)" width="170">
          <template #default="{ row }">
            <div>{{ memGiB(row.used_mem_mi) }} / {{ memGiB(row.mem_mi) }} GiB</div>
            <el-progress :percentage="pct(row.used_mem_mi, row.mem_mi)" :stroke-width="4" color="#7c3aed" />
          </template>
        </el-table-column>
        <el-table-column label="存储 (用户配额/上限)" width="185">
          <template #default="{ row }">
            <div>{{ gi(row.allocated_storage_gi) }} / {{ gi(row.storage_gi) }} GiB</div>
            <el-progress :percentage="pct(row.allocated_storage_gi, row.storage_gi)" :stroke-width="4" color="#059669" />
            <div class="small muted">环境实占 {{ gi(row.used_storage_gi) }} GiB</div>
          </template>
        </el-table-column>
        <el-table-column label="成员" width="80" align="center">
          <template #default="{ row }">{{ row.member_count }}</template>
        </el-table-column>
        <el-table-column label="环境" width="80" align="center">
          <template #default="{ row }">{{ row.env_count }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.disabled ? 'info' : 'success'">{{ row.disabled ? '已停用' : '正常' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="160" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!loading && !units.length" description="还没有二级单位，点击右上角创建" />
    </div>

    <el-dialog v-model="dialog" :title="editing ? '编辑单位' : '新建二级单位'" width="560px" destroy-on-close>
      <el-form :model="form" label-width="110px">
        <el-form-item label="单位名称" required>
          <el-input v-model="form.name" placeholder="例如：材料科学学院" maxlength="40" />
        </el-form-item>
        <el-form-item label="单位标识" required>
          <el-input v-model="form.slug" :disabled="editing" placeholder="小写字母/数字/短横线，例如 materials"></el-input>
          <div class="small muted" style="margin-top: 4px">
            命名空间：research-unit-{{ form.slug || 'slug' }}
          </div>
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" maxlength="200" />
        </el-form-item>
        <el-form-item label="CPU 上限" required>
          <el-input-number v-model="form.cpu_cores" :min="0.1" :step="0.5" :precision="2" />
          <span class="unit">核</span>
        </el-form-item>
        <el-form-item label="内存上限" required>
          <el-input-number v-model="form.mem_gib" :min="0.5" :step="1" :precision="1" />
          <span class="unit">GiB</span>
        </el-form-item>
        <el-form-item label="存储上限" required>
          <el-input-number v-model="form.storage_gib" :min="1" :step="5" :precision="0" />
          <span class="unit">GiB</span>
        </el-form-item>
        <el-form-item v-if="editing" label="状态">
          <el-switch v-model="form.disabled" active-text="停用" inactive-text="正常" />
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
import { cpuCores, memGiB, gi, pct } from '@/utils'

const units = ref([])
const loading = ref(false)
const dialog = ref(false)
const editing = ref(null)
const saving = ref(false)
const limits = ref({})
const form = reactive({
  name: '', slug: '', description: '', cpu_cores: 4, mem_gib: 8, storage_gib: 50, disabled: false
})

const allocated = computed(() => {
  const sum = { cpu_milli: 0, mem_mi: 0, storage_gi: 0 }
  for (const u of units.value) {
    sum.cpu_milli += Number(u.cpu_milli || 0)
    sum.mem_mi += Number(u.mem_mi || 0)
    sum.storage_gi += Number(u.storage_gi || 0)
  }
  return sum
})

async function load() {
  loading.value = true
  try {
    units.value = await api.get('/admin/units')
    limits.value = await api.get('/admin/settings')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editing.value = null
  const remainCPU = Math.max(0.1, Number(limits.value.cpu_milli || 4000) - allocated.value.cpu_milli)
  const remainMem = Math.max(0.25, Number(limits.value.mem_mi || 8192) - allocated.value.mem_mi)
  const remainStorage = Math.max(1, Number(limits.value.storage_gi || 60) - allocated.value.storage_gi)
  Object.assign(form, {
    name: '', slug: '', description: '', disabled: false,
    cpu_cores: Math.min(4, remainCPU / 1000),
    mem_gib: Math.min(8, Math.round((remainMem / 1024) * 4) / 4),
    storage_gib: Math.min(50, remainStorage)
  })
  dialog.value = true
}

function openEdit(row) {
  editing.value = row
  Object.assign(form, {
    name: row.name,
    slug: row.slug,
    description: row.description,
    cpu_cores: Number(row.cpu_milli) / 1000,
    mem_gib: Number(row.mem_mi) / 1024,
    storage_gib: Number(row.storage_gi),
    disabled: row.disabled
  })
  dialog.value = true
}

async function save() {
  if (!form.name || !form.slug) {
    ElMessage.warning('请填写单位名称和标识')
    return
  }
  const payload = {
    name: form.name,
    slug: form.slug,
    description: form.description,
    cpu_milli: Math.round(Number(form.cpu_cores) * 1000),
    mem_mi: Math.round(Number(form.mem_gib) * 1024),
    storage_gi: Math.round(Number(form.storage_gib)),
    disabled: form.disabled
  }
  saving.value = true
  try {
    if (editing.value) {
      await api.patch(`/admin/units/${editing.value.id}`, payload)
      ElMessage.success('单位已更新')
    } else {
      await api.post('/admin/units', payload)
      ElMessage.success('单位已创建，命名空间与资源配额已就绪')
    }
    dialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function remove(row) {
  try {
    await ElMessageBox.confirm(
      `确定删除单位「${row.name}」吗？该操作会删除命名空间 ${row.namespace}，且不可恢复。`,
      '删除单位', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }
    )
    await api.delete(`/admin/units/${row.id}`)
    ElMessage.success('单位已删除')
    await load()
  } catch (e) {
    if (e !== 'cancel') { /* handled */ }
  }
}

onMounted(load)
</script>

<style scoped>
.unit { margin-left: 8px; color: #6b7280; font-size: 13px; }
</style>