<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">审计日志</h2>
        <p class="page-sub">登录与平台管理操作记录，按时间倒序排列；仅平台管理员可查看</p>
      </div>
      <el-button :loading="exporting" @click="exportCsv">
        <el-icon><Download /></el-icon>&nbsp;导出 CSV
      </el-button>
    </div>

    <div class="panel">
      <el-form :inline="true" :model="filters" class="filter-form" @submit.prevent>
        <el-form-item label="操作者">
          <el-input v-model="filters.username" placeholder="用户名，模糊匹配" clearable
            style="width: 160px" @keyup.enter="search" />
        </el-form-item>
        <el-form-item label="操作类型">
          <el-select v-model="filters.action" placeholder="全部" clearable filterable style="width: 180px">
            <el-option v-for="(label, key) in actionLabels" :key="key" :label="label" :value="key" />
          </el-select>
        </el-form-item>
        <el-form-item label="关键字">
          <el-input v-model="filters.keyword" placeholder="目标 / 详情" clearable
            style="width: 180px" @keyup.enter="search" />
        </el-form-item>
        <el-form-item label="时间范围">
          <el-date-picker v-model="dateRange" type="daterange" unlink-panels
            range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期"
            :shortcuts="shortcuts" style="width: 260px" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

      <el-table :data="logs" v-loading="loading" size="small" row-key="id">
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column label="时间" width="175">
          <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作者" width="150">
          <template #default="{ row }">
            <div style="font-weight: 600">{{ row.username || '-' }}</div>
            <div class="small muted mono">ID {{ row.user_id }}</div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="140">
          <template #default="{ row }">
            <el-tag size="small" :type="tagType(row.action)">{{ actionLabels[row.action] || row.action }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="目标对象" min-width="180">
          <template #default="{ row }">{{ row.target || '-' }}</template>
        </el-table-column>
        <el-table-column label="详情" min-width="220">
          <template #default="{ row }">
            <span class="mono small">{{ row.detail || '-' }}</span>
          </template>
        </el-table-column>
      </el-table>

      <div class="pager">
        <el-pagination background layout="total, sizes, prev, pager, next, jumper"
          :total="total" v-model:current-page="page" v-model:page-size="pageSize"
          :page-sizes="[20, 50, 100, 200]" @size-change="onSizeChange" @current-change="load" />
      </div>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { fmtTime } from '@/utils'

const actionLabels = {
  login: '登录',
  change_password: '修改密码',
  update_platform_limits: '更新平台限额',
  create_user: '新建用户',
  update_user: '编辑用户',
  delete_user: '删除用户',
  create_template: '新建模板',
  update_template: '编辑模板',
  delete_template: '删除模板',
  create_unit: '新建单位',
  update_unit: '编辑单位',
  delete_unit: '删除单位',
  upsert_member: '添加成员',
  update_member: '调整成员配额',
  remove_member: '移除成员',
  create_environment: '新建环境',
  update_environment: '编辑环境',
  start_environment: '启动环境',
  stop_environment: '停止环境',
  restart_environment: '重启环境',
  delete_environment: '删除环境',
  create_snapshot: '创建快照',
  delete_snapshot: '删除快照',
  restore_snapshot: '恢复快照'
}

const shortcuts = [
  {
    text: '今天',
    value: () => {
      const end = new Date()
      const start = new Date()
      start.setHours(0, 0, 0, 0)
      return [start, end]
    }
  },
  {
    text: '最近 7 天',
    value: () => {
      const end = new Date()
      const start = new Date()
      start.setDate(start.getDate() - 6)
      start.setHours(0, 0, 0, 0)
      end.setHours(23, 59, 59, 999)
      return [start, end]
    }
  },
  {
    text: '最近 30 天',
    value: () => {
      const end = new Date()
      const start = new Date()
      start.setDate(start.getDate() - 29)
      start.setHours(0, 0, 0, 0)
      end.setHours(23, 59, 59, 999)
      return [start, end]
    }
  }
]

const logs = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const loading = ref(false)
const exporting = ref(false)
const filters = reactive({ username: '', action: '', keyword: '' })
const dateRange = ref([])

function queryParams() {
  const params = {
    page: page.value,
    page_size: pageSize.value,
    username: filters.username,
    action: filters.action,
    keyword: filters.keyword
  }
  if (Array.isArray(dateRange.value) && dateRange.value[0] && dateRange.value[1]) {
    const from = new Date(dateRange.value[0])
    const to = new Date(dateRange.value[1])
    from.setHours(0, 0, 0, 0)
    to.setHours(23, 59, 59, 999)
    params.from = from.toISOString()
    params.to = to.toISOString()
  }
  return params
}

function tagType(action) {
  if (!action) return 'info'
  if (action.includes('delete') || action.includes('remove') || action.includes('stop')) return 'danger'
  if (action.includes('create') || action.includes('start') || action.includes('restore')) return 'success'
  if (action.includes('update') || action.includes('change')) return 'warning'
  return 'primary'
}

async function load() {
  loading.value = true
  try {
    const data = await api.get('/admin/audit-logs', { params: queryParams() })
    logs.value = data.items || []
    total.value = data.total || 0
  } catch {} finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  load()
}

function onSizeChange() {
  page.value = 1
  load()
}

function reset() {
  Object.assign(filters, { username: '', action: '', keyword: '' })
  dateRange.value = []
  search()
}

async function exportCsv() {
  exporting.value = true
  try {
    const base = queryParams()
    delete base.page
    delete base.page_size
    const first = await api.get('/admin/audit-logs', { params: { ...base, page: 1, page_size: 500 } })
    const all = [...(first.items || [])]
    const pages = Math.ceil((first.total || 0) / 500)
    for (let p = 2; p <= pages && p <= 20; p++) {
      const data = await api.get('/admin/audit-logs', { params: { ...base, page: p, page_size: 500 } })
      all.push(...(data.items || []))
    }
    if (all.length < (first.total || 0)) {
      ElMessage.warning(`记录较多，仅导出前 ${all.length} 条，请缩小时间范围后重试`)
    }
    const esc = (v) => `"${String(v ?? '').replace(/"/g, '""')}"`
    const rows = [['ID', '时间', '操作者ID', '操作者', '操作', '目标对象', '详情']]
    for (const l of all) {
      rows.push([l.id, fmtTime(l.created_at), l.user_id, l.username, actionLabels[l.action] || l.action, l.target, l.detail])
    }
    const csv = '\uFEFF' + rows.map((r) => r.map(esc).join(',')).join('\r\n')
    const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv;charset=utf-8' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `审计日志_${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(url)
  } catch {} finally {
    exporting.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.filter-form {
  margin-bottom: 4px;
}
.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
