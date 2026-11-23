<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">科研环境</h2>
        <p class="page-sub">
          {{ auth.isAdmin
            ? '平台内全部科研环境，仅供查看与运维；新建环境请由二级单位成员操作'
            : (auth.unitAdminUnits.length
              ? '仅显示您个人创建的环境；单位内全部环境请前往「单位管理」查看'
              : '基于容器模板创建科研环境，通过 SSH 或 noVNC 桌面使用') }}
        </p>
      </div>
      <div>
        <el-button @click="load" :loading="loading"><el-icon><Refresh /></el-icon>&nbsp;刷新</el-button>
        <el-button v-if="!auth.isAdmin" type="primary" @click="openCreate"><el-icon><Plus /></el-icon>&nbsp;新建环境</el-button>
      </div>
    </div>

    <div class="panel">
      <EnvTable :envs="envs" :loading="loading" @reload="load" />
    </div>

    <el-dialog v-model="createDialog" title="新建科研环境" width="580px" destroy-on-close>
      <el-form :model="form" label-width="100px">
        <div v-if="selectedQuota" class="quota-hint">
          资源配额：CPU {{ cpuCores(selectedQuota.cpu) }} 核 · 内存 {{ memGiB(selectedQuota.mem) }} GiB · 存储 {{ gi(selectedQuota.storage) }} GiB
          <span class="muted">（运行中已用 CPU {{ cpuCores(selectedQuota.used_cpu) }} 核 / 内存 {{ memGiB(selectedQuota.used_mem) }} GiB；全部环境已分配存储 {{ gi(selectedQuota.used_storage) }} GiB）</span>
          <div class="small muted" style="margin-top: 3px">
            单个环境的 CPU/内存不能超过配额，全部环境存储合计不能超过配额；创建时不占用运行中额度，可创建多个环境，启动时再校验运行资源。
          </div>
        </div>
        <el-alert v-if="quotaBlocked" type="warning" :closable="false" show-icon style="margin-bottom: 14px"
          :title="quotaBlocked" />
        <el-form-item label="所属单位" required>
          <el-select v-model="form.unit_id" placeholder="选择单位" style="width: 100%" @change="applyTemplateDefaults">
            <el-option v-for="u in unitOptions" :key="u.id" :label="u.name" :value="u.id">
              <span>{{ u.name }}</span>
              <span class="small muted" style="float: right">CPU {{ cpuCores(u.cpu_milli) }} 核 / 内存 {{ memGiB(u.mem_mi) }} GiB</span>
            </el-option>
          </el-select>
        </el-form-item>
        <el-form-item label="容器模板" required>
          <el-select v-model="form.template_id" placeholder="选择模板" style="width: 100%" @change="onTemplateChange">
            <el-option v-for="t in templates" :key="t.id" :label="t.name" :value="t.id">
              <span>{{ t.name }}</span>
              <span style="float: right">
                <el-tag v-if="t.rootpv" size="small" type="success" style="margin-left: 4px">持久化</el-tag>
                <el-tag v-if="t.ephemeral" size="small" type="info" style="margin-left: 4px">临时</el-tag>
              </span>
            </el-option>
          </el-select>
          <div v-if="selectedTemplate" class="small muted" style="margin-top: 4px">
            {{ selectedTemplate.description }}（镜像 {{ selectedTemplate.image }}）
          </div>
        </el-form-item>
        <el-form-item v-if="selectedTemplate" label="环境模式" required>
          <el-radio-group v-model="form.rootpv" @change="applyTemplateDefaults">
            <el-radio v-if="selectedTemplate.rootpv" :value="true">持久化</el-radio>
            <el-radio v-if="selectedTemplate.ephemeral" :value="false">临时</el-radio>
          </el-radio-group>
          <div class="small muted" style="width: 100%; margin-top: 4px">
            {{ form.rootpv
              ? '根目录写入独立 PVC，停止或重建后数据保留，并占用存储配额'
              : '根目录随 Pod 生命周期，删除环境后数据不保留，不占用存储配额' }}
          </div>
        </el-form-item>
        <el-form-item label="环境名称">
          <el-input v-model="form.name" maxlength="64" placeholder="可选，例如 分子动力学实验" />
        </el-form-item>
        <el-form-item label="root 密码">
          <el-input v-model="form.root_password" type="password" show-password maxlength="64"
            placeholder="留空自动生成 14 位随机密码" />
          <div class="small muted" style="width: 100%; margin-top: 4px">
            通过环境变量 ROOT_PW 注入，用于 SSH / WebSSH 登录
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
            placeholder="留空自动生成 8 位随机密码" />
          <div class="small muted" style="width: 100%; margin-top: 4px">
            通过环境变量 VNC_PW 注入，VNC 协议最多 8 位
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
          <el-input-number v-model="form.storage_gib" :min="1" :step="5" :precision="0" /><span class="unit">GiB</span>
          <div class="small muted" style="width: 100%; margin-top: 4px">
            根目录 / 下的所有修改会持久化到独立 PVC，停止或重建后保留
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createDialog = false">取消</el-button>
        <el-button type="primary" :loading="creating" :disabled="!!quotaBlocked" @click="create">创建环境</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { useAuth } from '@/stores/auth'
import { cpuCores, memGiB, gi } from '@/utils'
import EnvTable from '@/components/EnvTable.vue'

const auth = useAuth()
const envs = ref([])
const templates = ref([])
const units = ref([])
const memberships = ref([])
const loading = ref(false)
const createDialog = ref(false)
const creating = ref(false)
let timer = null

const form = reactive({
  unit_id: null, template_id: null, name: '', cpu_cores: 2, mem_gib: 4, storage_gib: 10,
  root_password: '', graphical: true, vnc_password: '', shm_enabled: false, shm_mi: 1024, rootpv: true
})

const shmMaxMi = computed(() => Math.max(1, Math.floor(Number(form.mem_gib || 0) * 512)))
watch(() => form.mem_gib, () => {
  if (form.shm_enabled && form.shm_mi > shmMaxMi.value) form.shm_mi = shmMaxMi.value
})

const unitOptions = computed(() => units.value.map((u) => ({ ...u })))
const selectedTemplate = computed(() => templates.value.find((t) => t.id === form.template_id))
const selectedQuota = computed(() => {
  if (!form.unit_id) return null
  if (auth.isAdmin) {
    const u = units.value.find((x) => x.id === form.unit_id)
    if (!u) return null
    return {
      cpu: u.cpu_milli, mem: u.mem_mi, storage: u.storage_gi,
      used_cpu: u.used_cpu_milli, used_mem: u.used_mem_mi, used_storage: u.used_storage_gi
    }
  }
  const m = memberships.value.find((x) => x.unit_id === form.unit_id)
  if (!m) return null
  return { cpu: m.cpu_milli, mem: m.mem_mi, storage: m.storage_gi, used_cpu: m.used_cpu_milli, used_mem: m.used_mem_mi, used_storage: m.used_storage_gi }
})
const quotaBlocked = computed(() => {
  const q = selectedQuota.value
  if (!q) return ''
  if (form.rootpv && (q.storage - (q.used_storage || 0)) <= 0) {
    return '全部环境的存储额度已用尽，请删除不再使用的环境或联系管理员调整上限'
  }
  return ''
})

async function load() {
  loading.value = true
  try {
    envs.value = await api.get('/environments')
  } finally {
    loading.value = false
  }
}

async function loadOptions() {
  templates.value = await api.get('/templates')
  if (auth.isAdmin) {
    units.value = await api.get('/admin/units')
  } else {
    const me = await auth.fetchMe()
    memberships.value = me.memberships || []
    units.value = memberships.value.map((m) => ({
      id: m.unit_id,
      name: m.unit_name,
      cpu_milli: m.cpu_milli,
      mem_mi: m.mem_mi,
      storage_gi: m.storage_gi,
      used_cpu_milli: m.used_cpu_milli,
      used_mem_mi: m.used_mem_mi,
      used_storage_gi: m.used_storage_gi
    }))
  }
}

async function openCreate() {
  if (auth.isAdmin) {
    ElMessage.warning('平台管理员仅可查看与运维环境，新建环境请由二级单位成员操作')
    return
  }
  try {
    await loadOptions()
  } catch {
    return
  }
  if (!unitOptions.value.length) {
    ElMessage.warning('您还没有加入任何单位，请联系管理员')
    return
  }
  form.unit_id = unitOptions.value[0].id
  form.template_id = templates.value[0]?.id || null
  form.name = ''
  form.cpu_cores = 2
  form.mem_gib = 4
  form.storage_gib = 10
  form.root_password = ''
  form.graphical = true
  form.vnc_password = ''
  form.shm_enabled = false
  form.shm_mi = 1024
  form.rootpv = true
  applyTemplateDefaults()
  createDialog.value = true
}

function onTemplateChange() {
  applyTemplateDefaults()
}

function onShmToggle(enabled) {
  if (enabled && (!form.shm_mi || form.shm_mi > shmMaxMi.value)) {
    form.shm_mi = Math.min(1024, shmMaxMi.value)
  }
}

function applyTemplateDefaults() {
  const t = selectedTemplate.value
  if (!t) return
  if (form.rootpv && !t.rootpv) form.rootpv = false
  if (!form.rootpv && !t.ephemeral) form.rootpv = true
  const q = selectedQuota.value
  const maxCPU = q ? q.cpu : t.cpu_milli
  const maxMem = q ? q.mem : t.mem_mi
  const freeStorage = q ? Math.max(0, q.storage - (q.used_storage || 0)) : (Number(t.storage_gi) || 1)
  form.cpu_cores = Math.max(0.1, Math.min(Number(t.cpu_milli), maxCPU) / 1000)
  form.mem_gib = Math.max(0.25, Math.min(Number(t.mem_mi), maxMem) / 1024)
  form.storage_gib = form.rootpv
    ? Math.max(1, Math.min(Number(t.storage_gi) || 1, freeStorage || 1))
    : 0
}

async function create() {
  if (!form.unit_id || !form.template_id) {
    ElMessage.warning('请选择单位和模板')
    return
  }
  if (form.root_password && (form.root_password.length < 6 || form.root_password.length > 64)) {
    ElMessage.warning('root 密码长度需为 6-64 位')
    return
  }
  if (form.graphical && form.vnc_password && (form.vnc_password.length < 6 || form.vnc_password.length > 8)) {
    ElMessage.warning('VNC 密码长度需为 6-8 位')
    return
  }
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
  const t = selectedTemplate.value
  if (!t) {
    ElMessage.warning('请选择容器模板')
    return
  }
  if (form.rootpv && !t.rootpv) {
    ElMessage.warning('该模板不支持持久化模式')
    return
  }
  if (!form.rootpv && !t.ephemeral) {
    ElMessage.warning('该模板不支持临时模式')
    return
  }
  const rootPV = !!form.rootpv
  const storage = rootPV ? Math.round(Number(form.storage_gib)) : 0
  if (rootPV && storage <= 0) {
    ElMessage.warning('持久化模式必须配置大于 0 的存储容量')
    return
  }
  const q = selectedQuota.value
  if (q) {
    if (cpu > q.cpu || mem > q.mem) {
      ElMessage.warning('单个环境的 CPU / 内存不能超过个人资源上限')
      return
    }
    if (storage > q.storage - (q.used_storage || 0)) {
      ElMessage.warning('全部环境的存储总量将超出个人上限')
      return
    }
  }
  creating.value = true
  try {
    await api.post('/environments', {
      name: form.name,
      cpu_milli: cpu,
      mem_mi: mem,
      storage_gi: storage,
      root_password: form.root_password || undefined,
      graphical: form.graphical,
      vnc_password: form.graphical ? form.vnc_password || undefined : undefined,
      shm_enabled: !!form.shm_enabled,
      shm_mi: shmMi,
      unit_id: form.unit_id,
      template_id: form.template_id,
      rootpv: rootPV
    })
    createDialog.value = false
    ElMessage.success('环境已创建，尚未启动；请在列表中点击「启动」后连接')
    await load()
  } finally {
    creating.value = false
  }
}

onMounted(async () => {
  await loadOptions()
  await load()
  timer = setInterval(() => {
    if (document.visibilityState === 'visible') load()
  }, 10000)
})
onBeforeUnmount(() => timer && clearInterval(timer))
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