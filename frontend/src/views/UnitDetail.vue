<template>
  <div class="page" v-loading="loading">
    <template v-if="unit">
      <div class="page-header">
        <div>
          <h2 class="page-title">
            {{ unit.name }}
            <el-tag v-if="unit.disabled" size="small" type="info" style="margin-left: 8px">已停用</el-tag>
          </h2>
          <p class="page-sub">
            命名空间 <span class="mono">{{ unit.namespace }}</span> · {{ unit.description || '暂无描述' }}
          </p>
        </div>
        <div>
          <el-button @click="load"><el-icon><Refresh /></el-icon>&nbsp;刷新</el-button>
        </div>
      </div>

      <div class="stat-grid">
        <div class="stat-card">
          <div class="label">CPU 运行中 / 上限</div>
          <div class="value">{{ cpuCores(unitUsage.cpu_milli) }} <span class="small muted">/ {{ cpuCores(unit.cpu_milli) }} 核</span></div>
          <el-progress :percentage="pct(unitUsage.cpu_milli, unit.cpu_milli)" :stroke-width="6" style="margin-top: 10px" />
        </div>
        <div class="stat-card">
          <div class="label">内存 运行中 / 上限</div>
          <div class="value">{{ memGiB(unitUsage.mem_mi) }} <span class="small muted">/ {{ memGiB(unit.mem_mi) }} GiB</span></div>
          <el-progress :percentage="pct(unitUsage.mem_mi, unit.mem_mi)" :stroke-width="6" color="#7c3aed" style="margin-top: 10px" />
        </div>
        <div class="stat-card">
          <div class="label">存储 已分配（用户配额） / 上限</div>
          <div class="value">{{ gi(unitUsage.storage_allocated_gi) }} <span class="small muted">/ {{ gi(unit.storage_gi) }} GiB</span></div>
          <el-progress :percentage="pct(unitUsage.storage_allocated_gi, unit.storage_gi)" :stroke-width="6" color="#059669" style="margin-top: 10px" />
          <div class="hint">环境实际占用 {{ gi(unitUsage.storage_gi) }} GiB</div>
        </div>
        <div class="stat-card">
          <div class="label">成员 / 环境</div>
          <div class="value">{{ members.length }} <span class="small muted">/ {{ envs.length }}</span></div>
          <div class="hint">环境运行中 {{ envs.filter(e => e.status === 'running').length }} 个</div>
        </div>
      </div>

      <el-tabs v-model="tab" v-if="canManage">
        <el-tab-pane label="成员管理" name="members">
          <div class="panel">
            <div class="toolbar">
              <div class="grow"></div>
              <el-button type="primary" @click="openAddMember"><el-icon><Plus /></el-icon>&nbsp;添加成员</el-button>
            </div>
            <el-table :data="members" size="default">
              <el-table-column label="成员" min-width="170">
                <template #default="{ row }">
                  <div style="font-weight: 600">
                    {{ row.display_name || row.username }}
                    <el-tag v-if="row.user_id === auth.user?.id" size="small" type="info" style="margin-left: 6px">本人</el-tag>
                  </div>
                  <div class="small muted mono">{{ row.username }}</div>
                </template>
              </el-table-column>
              <el-table-column label="角色" width="120">
                <template #default="{ row }">
                  <el-tag size="small" :type="row.role === 'unit_admin' ? 'warning' : 'info'">
                    {{ row.role === 'unit_admin' ? '二级管理员' : '科研人员' }}
                  </el-tag>
                </template>
              </el-table-column>
              <el-table-column label="CPU (运行中/上限)" width="160">
                <template #default="{ row }">
                  {{ cpuCores(row.used_cpu_milli) }} / {{ cpuCores(row.cpu_milli) }} 核
                  <el-progress :percentage="pct(row.used_cpu_milli, row.cpu_milli)" :stroke-width="4" />
                </template>
              </el-table-column>
              <el-table-column label="内存 (运行中/上限)" width="165">
                <template #default="{ row }">
                  {{ memGiB(row.used_mem_mi) }} / {{ memGiB(row.mem_mi) }} GiB
                  <el-progress :percentage="pct(row.used_mem_mi, row.mem_mi)" :stroke-width="4" color="#7c3aed" />
                </template>
              </el-table-column>
              <el-table-column label="存储 (已分配/上限)" width="165">
                <template #default="{ row }">
                  {{ gi(row.used_storage_gi) }} / {{ gi(row.storage_gi) }} GiB
                  <el-progress :percentage="pct(row.used_storage_gi, row.storage_gi)" :stroke-width="4" color="#059669" />
                </template>
              </el-table-column>
              <el-table-column label="环境" width="70" align="center">
                <template #default="{ row }">{{ row.env_count }}</template>
              </el-table-column>
              <el-table-column label="操作" width="150" fixed="right">
                <template #default="{ row }">
                  <el-button link type="primary" @click="openEditMember(row)">编辑配额</el-button>
                  <el-button v-if="row.user_id !== auth.user?.id" link type="danger" @click="removeMember(row)">移除</el-button>
                </template>
              </el-table-column>
            </el-table>
            <el-empty v-if="!members.length" description="暂无成员，点击添加" />
          </div>
        </el-tab-pane>

        <el-tab-pane label="科研环境" name="envs">
          <div class="panel">
            <div class="toolbar">
              <div class="grow"></div>
              <el-button @click="loadEnvs" :loading="envLoading"><el-icon><Refresh /></el-icon>&nbsp;刷新</el-button>
            </div>
            <EnvTable :envs="envs" :loading="envLoading" empty-text="该单位还没有科研环境" @reload="loadEnvs" />
          </div>
        </el-tab-pane>
      </el-tabs>

      <div v-else class="panel">
        <el-alert type="info" :closable="false" show-icon
          title="您是本单位成员。环境创建与连接请前往「科研环境」页面，配额调整请联系二级管理员。" />
      </div>
    </template>

    <el-dialog v-model="memberDialog" :title="editingMember ? '编辑成员' : '添加成员'" width="540px" destroy-on-close>
      <el-form :model="memberForm" label-width="105px">
        <el-form-item label="用户名" required>
          <el-input v-model="memberForm.username" :disabled="!!editingMember" placeholder="已有账号直接填写用户名；新用户将自动创建" />
        </el-form-item>
        <el-form-item v-if="!editingMember" label="初始密码">
          <el-input v-model="memberForm.password" type="password" show-password placeholder="新用户至少 8 位；已有用户留空" />
        </el-form-item>
        <el-form-item label="显示名称">
          <el-input v-model="memberForm.display_name" maxlength="40" :disabled="editingMember?.username === 'admin'" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="memberForm.role" style="width: 100%">
            <el-option label="科研人员" value="member" />
            <el-option label="二级管理员" value="unit_admin" />
          </el-select>
        </el-form-item>
        <el-form-item label="CPU 上限" required>
          <el-input-number v-model="memberForm.cpu_cores" :min="0" :step="0.5" :precision="2" /><span class="unit">核</span>
        </el-form-item>
        <el-form-item label="内存上限" required>
          <el-input-number v-model="memberForm.mem_gib" :min="0" :step="1" :precision="1" /><span class="unit">GiB</span>
        </el-form-item>
        <el-form-item label="存储上限">
          <el-input-number v-model="memberForm.storage_gib" :min="0" :step="5" :precision="0" /><span class="unit">GiB</span>
          <div class="small muted" style="width: 100%; margin-top: 4px">
            单位剩余可分配存储 {{ gi(memberStorageAvailable) }} GiB（全体成员存储配额合计不可超过单位上限）
          </div>
        </el-form-item>
        <el-form-item v-if="editingMember" label="重置密码">
          <el-input v-model="memberForm.password" type="password" show-password placeholder="留空则不修改" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="memberDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveMember">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'
import { useAuth } from '@/stores/auth'
import { cpuCores, memGiB, gi, pct } from '@/utils'
import EnvTable from '@/components/EnvTable.vue'

const auth = useAuth()
const route = useRoute()
const id = Number(route.params.id)
const unit = ref(null)
const unitUsage = ref({})
const canManage = ref(false)
const members = ref([])
const envs = ref([])
const loading = ref(false)
const envLoading = ref(false)
const tab = ref('members')
const memberDialog = ref(false)
const editingMember = ref(null)
const saving = ref(false)
let timer = null
const memberForm = reactive({
  username: '', password: '', display_name: '', role: 'member', cpu_cores: 2, mem_gib: 4, storage_gib: 10
})

const memberStorageAvailable = computed(() => {
  const total = Number(unit.value?.storage_gi || 0)
  const allocated = members.value.reduce((sum, m) => sum + Number(m.storage_gi || 0), 0)
  const own = editingMember.value ? Number(editingMember.value.storage_gi || 0) : 0
  return Math.max(0, total - (allocated - own))
})

async function loadEnvs() {
  if (!canManage.value) return
  envLoading.value = true
  try {
    envs.value = await api.get(`/units/${id}/environments`)
  } finally {
    envLoading.value = false
  }
}

async function load() {
  loading.value = true
  try {
    const data = await api.get(`/units/${id}`)
    unit.value = data.unit
    unitUsage.value = data.unit_usage || {}
    canManage.value = !!data.is_unit_admin
    if (canManage.value) {
      members.value = await api.get(`/units/${id}/members`)
      envs.value = await api.get(`/units/${id}/environments`)
    } else {
      const all = await api.get('/environments')
      envs.value = all.filter((e) => e.unit_id === id)
    }
  } finally {
    loading.value = false
  }
}

function openAddMember() {
  editingMember.value = null
  const u = unit.value || {}
  Object.assign(memberForm, {
    username: '', password: '', display_name: '', role: 'member',
    cpu_cores: Math.max(0.1, Math.min(2, Number(u.cpu_milli || 2000) / 1000)),
    mem_gib: Math.max(0.25, Math.min(4, Number(u.mem_mi || 4096) / 1024)),
    storage_gib: Math.min(10, memberStorageAvailable.value)
  })
  memberDialog.value = true
}

function openEditMember(row) {
  editingMember.value = row
  Object.assign(memberForm, {
    username: row.username,
    password: '',
    display_name: row.display_name,
    role: row.role,
    cpu_cores: Number(row.cpu_milli) / 1000,
    mem_gib: Number(row.mem_mi) / 1024,
    storage_gib: Number(row.storage_gi)
  })
  memberDialog.value = true
}

async function saveMember() {
  const payload = {
    role: memberForm.role,
    cpu_milli: Math.round(Number(memberForm.cpu_cores) * 1000),
    mem_mi: Math.round(Number(memberForm.mem_gib) * 1024),
    storage_gi: Math.round(Number(memberForm.storage_gib))
  }
  if (payload.storage_gi > memberStorageAvailable.value) {
    ElMessage.warning(`全体成员存储配额合计不能超过单位上限，当前最多还可分配 ${gi(memberStorageAvailable.value)} GiB`)
    return
  }
  saving.value = true
  try {
    if (editingMember.value) {
      await api.patch(`/units/${id}/members/${editingMember.value.user_id}`, {
        ...payload,
        password: memberForm.password || undefined,
        display_name: memberForm.display_name || undefined
      })
      ElMessage.success('成员配额已更新')
    } else {
      await api.post(`/units/${id}/members`, {
        ...payload,
        username: memberForm.username,
        password: memberForm.password,
        display_name: memberForm.display_name
      })
      ElMessage.success('成员已添加')
    }
    memberDialog.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function removeMember(row) {
  try {
    await ElMessageBox.confirm(
      `确定将「${row.display_name || row.username}」移出本单位吗？需先删除其科研环境。`,
      '移除成员', { type: 'warning', confirmButtonText: '移除', cancelButtonText: '取消' }
    )
    await api.delete(`/units/${id}/members/${row.user_id}`)
    ElMessage.success('成员已移除')
    await load()
  } catch {}
}

onMounted(async () => {
  await load()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible' && tab.value === 'envs') loadEnvs()
  }, 10000)
})
onBeforeUnmount(() => timer && clearInterval(timer))
</script>

<style scoped>
.unit { margin-left: 8px; color: #6b7280; font-size: 13px; }
</style>