<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">用户管理</h2>
        <p class="page-sub">平台账号、所属二级单位与账号状态（单位归属请在单位管理中调整）</p>
      </div>
      <el-button type="primary" @click="openCreate"><el-icon><Plus /></el-icon>&nbsp;新建用户</el-button>
    </div>

    <div class="panel">
      <el-table :data="users" v-loading="loading">
        <el-table-column label="用户" min-width="180">
          <template #default="{ row }">
            <div style="font-weight: 600">{{ row.display_name || row.username }}</div>
            <div class="small muted mono">{{ row.username }}</div>
          </template>
        </el-table-column>
        <el-table-column prop="email" label="邮箱" min-width="180">
          <template #default="{ row }">{{ row.email || '-' }}</template>
        </el-table-column>
        <el-table-column label="角色" width="130">
          <template #default="{ row }">
            <el-tag size="small" :type="row.is_platform_admin ? 'danger' : (row.is_unit_admin ? 'warning' : 'info')">
              {{ row.is_platform_admin ? '平台管理员' : (row.is_unit_admin ? '二级管理员' : '科研人员') }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="所属二级单位" min-width="190">
          <template #default="{ row }">
            <span v-if="row.unit_names">{{ row.unit_names }}</span>
            <span v-else class="muted">未加入二级单位</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="row.disabled ? 'info' : 'success'">{{ row.disabled ? '已禁用' : '正常' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="180">
          <template #default="{ row }">{{ fmtTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" :disabled="row.id === auth.user?.id"
              title="不能删除自己的账号" @click="removeUser(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialog" :title="editing ? '编辑用户' : '新建用户'" width="520px" destroy-on-close>
      <el-form :model="form" label-width="100px">
        <el-form-item label="用户名" required>
          <el-input v-model="form.username" :disabled="!!editing" placeholder="3-32 位字母/数字/下划线" />
        </el-form-item>
        <el-form-item label="显示名称">
          <el-input v-model="form.display_name" maxlength="40" />
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input v-model="form.email" maxlength="120" />
        </el-form-item>
        <el-form-item :label="editing ? '重置密码' : '初始密码'" :required="!editing">
          <el-input v-model="form.password" type="password" show-password :placeholder="editing ? '留空则不修改' : '至少 8 位'" />
        </el-form-item>
        <el-form-item label="身份类型" required>
          <el-select v-model="form.identity" style="width: 100%">
            <el-option label="平台管理员" value="platform_admin" />
            <el-option label="二级管理员" value="unit_admin" />
            <el-option label="科研人员" value="member" />
          </el-select>
          <div class="small muted" style="width: 100%; margin-top: 4px">
            {{ form.identity === 'platform_admin'
              ? '拥有集群资源与全部二级单位的管理权限'
              : (form.identity === 'unit_admin'
                ? '管理所属单位的人员与资源分配'
                : '在所属单位内创建和使用科研环境') }}
          </div>
        </el-form-item>
        <el-form-item v-if="!editing" label="所属单位" :required="form.identity !== 'platform_admin'">
          <el-select v-model="form.unit_id" :disabled="form.identity === 'platform_admin'" filterable
            placeholder="请选择二级单位" style="width: 100%">
            <el-option v-for="unit in units" :key="unit.id" :label="unit.name" :value="unit.id" />
          </el-select>
          <div v-if="!units.length" class="small muted" style="width: 100%; margin-top: 4px">
            暂无二级单位，请先在「二级单位」中创建
          </div>
        </el-form-item>
        <el-form-item v-else label="所属单位">
          <el-input :model-value="editing.unit_names || '未加入二级单位'" disabled />
          <div class="small muted" style="width: 100%; margin-top: 4px">
            单位信息不可修改；身份类型变更会同步到该用户所在单位
          </div>
          <div v-if="form.identity !== 'platform_admin' && !editing.unit_names" class="small muted" style="width: 100%; margin-top: 4px">
            该用户尚未加入二级单位；请先在单位管理中为其添加单位
          </div>
        </el-form-item>
        <el-form-item v-if="editing" label="禁用账号">
          <el-switch v-model="form.disabled" />
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
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import api from '@/api'
import { useAuth } from '@/stores/auth'
import { fmtTime } from '@/utils'

const auth = useAuth()
const users = ref([])
const units = ref([])
const loading = ref(false)
const dialog = ref(false)
const editing = ref(null)
const saving = ref(false)
const form = reactive({
  username: '', display_name: '', email: '', password: '', identity: 'member', unit_id: null, disabled: false
})

async function load() {
  loading.value = true
  try {
    users.value = await api.get('/admin/users')
  } finally {
    loading.value = false
  }
}

async function loadUnits() {
  units.value = await api.get('/admin/units')
}

function openCreate() {
  editing.value = null
  Object.assign(form, {
    username: '', display_name: '', email: '', password: '', identity: 'member', unit_id: null, disabled: false
  })
  dialog.value = true
}

function openEdit(row) {
  editing.value = row
  Object.assign(form, {
    username: row.username,
    display_name: row.display_name,
    email: row.email,
    password: '',
    identity: row.is_platform_admin ? 'platform_admin' : (row.is_unit_admin ? 'unit_admin' : 'member'),
    unit_id: null,
    disabled: row.disabled
  })
  dialog.value = true
}

async function save() {
  if (!form.username) {
    ElMessage.warning('请输入用户名')
    return
  }
  if (!editing.value && (!form.password || form.password.length < 8)) {
    ElMessage.warning('初始密码至少 8 位')
    return
  }
  if (form.identity !== 'platform_admin') {
    if (editing.value && !editing.value.unit_names) {
      ElMessage.warning('该用户未加入二级单位；编辑用户不能调整单位，请先在单位管理中为其添加单位')
      return
    }
    if (!editing.value && !form.unit_id) {
      ElMessage.warning('二级管理员和科研人员必须选择所属二级单位')
      return
    }
  }
  saving.value = true
  try {
    if (editing.value) {
      const payload = {
        display_name: form.display_name,
        email: form.email,
        role: form.identity,
        disabled: form.disabled
      }
      if (form.password) payload.password = form.password
      await api.patch(`/admin/users/${editing.value.id}`, payload)
      ElMessage.success('用户已更新')
    } else {
      const payload = {
        username: form.username,
        password: form.password,
        display_name: form.display_name,
        email: form.email,
        role: form.identity
      }
      if (form.identity !== 'platform_admin') payload.unit_id = form.unit_id
      await api.post('/admin/users', payload)
      ElMessage.success('用户已创建')
    }
    dialog.value = false
    await load()
    if (editing.value?.id === auth.user?.id) await auth.fetchMe()
  } finally {
    saving.value = false
  }
}

async function removeUser(row) {
  try {
    await ElMessageBox.confirm(
      `确定删除用户「${row.display_name || row.username}」吗？删除后该账号及其二级单位归属将一并移除，且不可恢复。`,
      '删除用户',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消', confirmButtonClass: 'el-button--danger' }
    )
  } catch {
    return
  }
  try {
    await api.delete(`/admin/users/${row.id}`)
    ElMessage.success('用户已删除')
    await load()
  } catch {}
}

onMounted(() => {
  load()
  loadUnits().catch(() => {})
})
</script>