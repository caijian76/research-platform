<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">平台设置</h2>
        <p class="page-sub">控制平台可分配给二级单位的总资源上限</p>
      </div>
    </div>

    <div class="panel" v-loading="loading" style="max-width: 720px">
      <h3 class="panel-title">平台总资源</h3>
      <el-alert type="info" :closable="false" show-icon style="margin-bottom: 18px">
        集群节点可分配：CPU {{ cpuCores(overview.cluster_cpu_milli) }} 核 · 内存 {{ memGiB(overview.cluster_mem_mi) }} GiB；
        已分配给单位：CPU {{ cpuCores(overview.allocated?.cpu_milli) }} 核 · 内存 {{ memGiB(overview.allocated?.mem_mi) }} GiB · 存储 {{ gi(overview.allocated?.storage_gi) }} GiB。
        平台上限不能低于已分配量。
      </el-alert>
      <el-form :model="form" label-width="130px">
        <el-form-item label="CPU 总上限" required>
          <el-input-number v-model="form.cpu_cores" :min="0.1" :step="1" :precision="2" />
          <span class="unit">核</span>
        </el-form-item>
        <el-form-item label="内存总上限" required>
          <el-input-number v-model="form.mem_gib" :min="0.5" :step="2" :precision="1" />
          <span class="unit">GiB</span>
        </el-form-item>
        <el-form-item label="存储总上限" required>
          <el-input-number v-model="form.storage_gib" :min="1" :step="10" :precision="0" />
          <span class="unit">GiB</span>
        </el-form-item>
        <el-form-item label="存储类">
          <el-tag>{{ overview.storage_class || '-' }}</el-tag>
          <span class="small muted" style="margin-left: 10px">科研环境 PVC 使用的 StorageClass（部署时配置）</span>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="save">保存设置</el-button>
        </el-form-item>
      </el-form>
    </div>
  </div>
</template>

<script setup>
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '@/api'
import { cpuCores, memGiB, gi } from '@/utils'

const loading = ref(false)
const saving = ref(false)
const overview = ref({})
const form = reactive({ cpu_cores: 8, mem_gib: 16, storage_gib: 500 })

async function load() {
  loading.value = true
  try {
    overview.value = await api.get('/admin/overview')
    const l = overview.value.platform_limits
    form.cpu_cores = Number(l.cpu_milli) / 1000
    form.mem_gib = Number(l.mem_mi) / 1024
    form.storage_gib = Number(l.storage_gi)
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    await api.put('/admin/settings', {
      cpu_milli: Math.round(Number(form.cpu_cores) * 1000),
      mem_mi: Math.round(Number(form.mem_gib) * 1024),
      storage_gi: Math.round(Number(form.storage_gib))
    })
    ElMessage.success('平台资源上限已更新')
    await load()
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.unit { margin-left: 8px; color: #6b7280; font-size: 13px; }
</style>