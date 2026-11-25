<template>
  <div class="layout">
    <aside class="sidebar">
      <div class="brand">
        <div class="logo">科</div>
        <div>
          <div class="name">科研云平台</div>
          <div class="env">Research Cloud · RootPV</div>
        </div>
      </div>
      <el-menu :default-active="activePath" router background-color="transparent" text-color="#cbd5e1">
        <el-menu-item index="/">
          <el-icon><DataBoard /></el-icon><span>工作台</span>
        </el-menu-item>
        <el-menu-item v-if="!auth.isAdmin" index="/environments">
          <el-icon><Monitor /></el-icon><span>科研环境</span>
        </el-menu-item>

        <el-sub-menu v-if="auth.unitAdminUnits.length" index="units-admin">
          <template #title><el-icon><OfficeBuilding /></el-icon><span>单位管理</span></template>
          <el-menu-item v-for="m in auth.unitAdminUnits" :key="m.unit_id" :index="`/unit/${m.unit_id}`">
            {{ m.unit_name }}
          </el-menu-item>
        </el-sub-menu>

        <template v-if="auth.isAdmin">
          <div class="menu-group">平台管理</div>
          <el-menu-item index="/environments">
            <el-icon><Monitor /></el-icon><span>科研环境</span>
          </el-menu-item>
          <el-menu-item index="/units">
            <el-icon><OfficeBuilding /></el-icon><span>二级单位</span>
          </el-menu-item>
          <el-menu-item index="/users">
            <el-icon><User /></el-icon><span>用户管理</span>
          </el-menu-item>
          <el-menu-item index="/templates">
            <el-icon><Box /></el-icon><span>容器模板</span>
          </el-menu-item>
          <el-menu-item index="/settings">
            <el-icon><Setting /></el-icon><span>平台设置</span>
          </el-menu-item>
          <el-menu-item index="/audit-logs">
            <el-icon><Document /></el-icon><span>审计日志</span>
          </el-menu-item>
        </template>
      </el-menu>
      <div class="sidebar-footer">v1.0 · K8s + RootPV</div>
    </aside>

    <div class="main">
      <header class="topbar">
        <div class="crumb">{{ pageTitle }}</div>
        <el-dropdown @command="onCommand">
          <div class="user-chip">
            <div class="avatar">{{ initial }}</div>
            <div>
              <div style="font-size: 13px; font-weight: 600">{{ auth.user?.display_name || auth.user?.username }}</div>
              <div class="small muted">
                {{ auth.isAdmin ? '平台管理员' : (auth.unitAdminUnits.length ? '二级管理员' : '科研人员') }}
              </div>
            </div>
            <el-icon><ArrowDown /></el-icon>
          </div>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="password">修改密码</el-dropdown-item>
              <el-dropdown-item divided command="logout">退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </header>
      <router-view />
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuth } from '@/stores/auth'

const auth = useAuth()
const route = useRoute()
const router = useRouter()

const titles = {
  dashboard: '工作台',
  environments: '科研环境',
  unit: '单位管理',
  units: '二级单位管理',
  users: '用户管理',
  templates: '容器模板',
  settings: '平台设置',
  'audit-logs': '审计日志',
  password: '修改密码'
}
const pageTitle = computed(() => titles[route.name] || '科研云平台')
const activePath = computed(() => {
  if (route.name === 'unit') return route.path
  return route.path
})
const initial = computed(() => (auth.user?.display_name || auth.user?.username || '?').slice(0, 1).toUpperCase())

function onCommand(cmd) {
  if (cmd === 'logout') {
    auth.logout()
    router.push('/login')
  } else if (cmd === 'password') {
    router.push('/password')
  }
}
</script>

<style scoped>
.menu-group {
  color: #475569; font-size: 11px; padding: 16px 20px 6px; letter-spacing: 1px; text-transform: uppercase;
}
</style>