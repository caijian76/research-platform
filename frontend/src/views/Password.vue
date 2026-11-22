<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">修改密码</h2>
        <p class="page-sub">账号：{{ auth.user?.username }}</p>
      </div>
    </div>
    <div class="panel" style="max-width: 480px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="原密码" required>
          <el-input v-model="form.old_password" type="password" show-password />
        </el-form-item>
        <el-form-item label="新密码" required>
          <el-input v-model="form.new_password" type="password" show-password placeholder="至少 8 位" />
        </el-form-item>
        <el-form-item label="确认密码" required>
          <el-input v-model="form.confirm" type="password" show-password />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">更新密码</el-button>
        </el-form-item>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { useAuth } from '@/stores/auth'

const auth = useAuth()
const saving = ref(false)
const form = reactive({ old_password: '', new_password: '', confirm: '' })

async function save() {
  if (form.new_password.length < 8) {
    ElMessage.warning('新密码至少 8 位')
    return
  }
  if (form.new_password !== form.confirm) {
    ElMessage.warning('两次输入的新密码不一致')
    return
  }
  saving.value = true
  try {
    await api.post('/me/password', { old_password: form.old_password, new_password: form.new_password })
    ElMessage.success('密码已更新')
    form.old_password = ''
    form.new_password = ''
    form.confirm = ''
  } finally {
    saving.value = false
  }
}
</script>