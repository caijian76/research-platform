<template>
  <div class="login-wrap">
    <div class="login-hero">
      <div class="brand" style="border: none; padding: 0; height: auto">
        <div class="logo">科</div>
        <div>
          <div class="name" style="font-size: 17px">科研云平台</div>
          <div class="env">Research Cloud Platform</div>
        </div>
      </div>
      <h1>为科研人员提供<br />开箱即用的持久化容器环境</h1>
      <p>
        基于 Kubernetes 与 RootPV 持久化根文件系统，几分钟内获得带 SSH 与图形桌面（noVNC）的科研工作空间。
        平台按二级单位隔离命名空间，支持多级资源配额管理。
      </p>
      <div class="hero-badges">
        <span>RootPV 根目录持久化</span>
        <span>SSH 直连</span>
        <span>noVNC 桌面</span>
        <span>单位级资源配额</span>
        <span>模板化交付</span>
      </div>
    </div>
    <div class="login-panel">
      <div class="login-card">
        <h2>登录平台</h2>
        <div class="sub">使用平台或二级单位分配的账号登录</div>
        <el-form :model="form" @submit.prevent="submit" label-position="top">
          <el-form-item label="用户名">
            <el-input v-model="form.username" size="large" placeholder="请输入用户名" autocomplete="username">
              <template #prefix><el-icon><User /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="form.password" type="password" size="large" show-password placeholder="请输入密码"
              autocomplete="current-password" @keyup.enter="submit">
              <template #prefix><el-icon><Lock /></el-icon></template>
            </el-input>
          </el-form-item>
          <el-button type="primary" size="large" style="width: 100%; margin-top: 8px" :loading="loading" @click="submit">
            登录
          </el-button>
        </el-form>
        <div class="login-tip">
          初始平台管理员账号由部署时配置（默认 admin）。若忘记密码，请联系平台管理员重置。
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useAuth } from '@/stores/auth'

const auth = useAuth()
const router = useRouter()
const route = useRoute()
const loading = ref(false)
const form = reactive({ username: '', password: '' })

async function submit() {
  if (!form.username || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await auth.login(form.username.trim(), form.password)
    ElMessage.success('登录成功')
    router.replace(route.query.redirect || '/')
  } catch (e) {
    // interceptor already shows message
  } finally {
    loading.value = false
  }
}
</script>