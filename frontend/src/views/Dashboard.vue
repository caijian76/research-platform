<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h2 class="page-title">{{ auth.isAdmin ? '平台工作台' : '我的工作台' }}</h2>
        <p class="page-sub">
          {{ auth.isAdmin
            ? '集群资源、二级单位分配与环境运行概览'
            : (auth.unitAdminUnits.length
              ? '仅显示您的个人环境与配额，单位全量信息请在「单位管理」中查看'
              : '查看您的单位配额并快速进入科研环境') }}
        </p>
      </div>
      <div>
        <el-button v-if="!auth.isAdmin" type="primary" @click="$router.push('/environments')">
          <el-icon><Plus /></el-icon>&nbsp;新建科研环境
        </el-button>
        <el-button v-else @click="$router.push('/environments')">
          <el-icon><Monitor /></el-icon>&nbsp;查看科研环境
        </el-button>
      </div>
    </div>

    <template v-if="auth.isAdmin">
      <div class="stat-grid" v-loading="loading">
        <div class="stat-card">
          <div class="stat-icon"><Cpu /></div>
          <div class="label">CPU 已分配 / 平台上限</div>
          <div class="value">{{ cpuCores(overview.allocated?.cpu_milli) }} <span class="small muted">/ {{ cpuCores(overview.platform_limits?.cpu_milli) }} 核</span></div>
          <el-progress :percentage="pct(overview.allocated?.cpu_milli, overview.platform_limits?.cpu_milli)" :stroke-width="6" style="margin-top: 10px" />
          <div class="hint">集群可分配 {{ cpuCores(overview.cluster_cpu_milli) }} 核</div>
        </div>
        <div class="stat-card">
          <div class="stat-icon" style="color: #7c3aed; background: #f5f3ff"><Coin /></div>
          <div class="label">内存 已分配 / 平台上限</div>
          <div class="value">{{ memGiB(overview.allocated?.mem_mi) }} <span class="small muted">/ {{ memGiB(overview.platform_limits?.mem_mi) }} GiB</span></div>
          <el-progress :percentage="pct(overview.allocated?.mem_mi, overview.platform_limits?.mem_mi)" :stroke-width="6" color="#7c3aed" style="margin-top: 10px" />
          <div class="hint">集群可分配 {{ memGiB(overview.cluster_mem_mi) }} GiB</div>
        </div>
        <div class="stat-card">
          <div class="stat-icon" style="color: #059669; background: #ecfdf5"><FolderOpened /></div>
          <div class="label">存储 已分配 / 平台上限</div>
          <div class="value">{{ gi(overview.allocated?.storage_gi) }} <span class="small muted">/ {{ gi(overview.platform_limits?.storage_gi) }} GiB</span></div>
          <el-progress :percentage="pct(overview.allocated?.storage_gi, overview.platform_limits?.storage_gi)" :stroke-width="6" color="#059669" style="margin-top: 10px" />
          <div class="hint">StorageClass: {{ overview.storage_class }}</div>
        </div>
        <div class="stat-card">
          <div class="stat-icon" style="color: #ea580c; background: #fff7ed"><OfficeBuilding /></div>
          <div class="label">二级单位 / 用户 / 环境</div>
          <div class="value">{{ overview.unit_count || 0 }} <span class="small muted">/ {{ overview.user_count || 0 }} / {{ overview.env_count || 0 }}</span></div>
          <div class="hint">运行中环境 {{ overview.running_env_count || 0 }} 个</div>
        </div>
      </div>

      <div class="panel">
        <h3 class="panel-title">集群节点 <span class="panel-sub">共 {{ overview.nodes?.length || 0 }} 个节点</span></h3>
        <el-table :data="overview.nodes || []" size="small">
          <el-table-column prop="name" label="节点" min-width="160" />
          <el-table-column label="角色" width="130">
            <template #default="{ row }">
              <el-tag size="small" :type="row.role === 'control-plane' ? 'warning' : 'info'">{{ row.role }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag size="small" :type="row.ready ? 'success' : 'danger'">{{ row.ready ? 'Ready' : 'NotReady' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="可分配 CPU" width="150">
            <template #default="{ row }">{{ cpuCores(row.cpu_milli) }} 核</template>
          </el-table-column>
          <el-table-column label="可分配内存" width="150">
            <template #default="{ row }">{{ memGiB(row.mem_mi) }} GiB</template>
          </el-table-column>
        </el-table>
      </div>

      <div class="panel">
        <h3 class="panel-title">管理入口</h3>
        <div class="quick-grid">
          <div class="quick" @click="$router.push('/environments')">
            <el-icon><Monitor /></el-icon>
            <div><b>科研环境</b><span>查看与运维平台内全部环境</span></div>
          </div>
          <div class="quick" @click="$router.push('/units')">
            <el-icon><OfficeBuilding /></el-icon>
            <div><b>二级单位</b><span>创建单位、分配资源上限</span></div>
          </div>
          <div class="quick" @click="$router.push('/users')">
            <el-icon><User /></el-icon>
            <div><b>用户管理</b><span>平台账号与管理员权限</span></div>
          </div>
          <div class="quick" @click="$router.push('/templates')">
            <el-icon><Box /></el-icon>
            <div><b>容器模板</b><span>镜像、规格与 RootPV 配置</span></div>
          </div>
          <div class="quick" @click="$router.push('/settings')">
            <el-icon><Setting /></el-icon>
            <div><b>平台设置</b><span>平台总资源上限</span></div>
          </div>
        </div>
      </div>
    </template>

    <template v-else>
      <div class="stat-grid">
        <div class="stat-card">
          <div class="stat-icon"><Monitor /></div>
          <div class="label">我的科研环境</div>
          <div class="value">{{ envs.length }}</div>
          <div class="hint">运行中 {{ runningCount }} 个</div>
        </div>
        <div class="stat-card">
          <div class="stat-icon" style="color: #059669; background: #ecfdf5"><OfficeBuilding /></div>
          <div class="label">所属单位</div>
          <div class="value">{{ auth.memberships.length }}</div>
          <div class="hint">{{ auth.unitAdminUnits.length ? `管理 ${auth.unitAdminUnits.length} 个单位` : '科研人员' }}</div>
        </div>
      </div>

      <div class="panel">
        <h3 class="panel-title">单位与个人资源配额</h3>
        <el-empty v-if="!auth.memberships.length" description="您还未加入任何单位，请联系平台管理员" />
        <div v-for="m in auth.memberships" :key="m.unit_id" class="quota-row">
          <div class="quota-head">
            <div>
              <b>{{ m.unit_name }}</b>
              <el-tag size="small" :type="m.role === 'unit_admin' ? 'warning' : 'info'" style="margin-left: 8px">
                {{ m.role === 'unit_admin' ? '二级管理员' : '科研人员' }}
              </el-tag>
            </div>
            <div>
              <el-button v-if="m.role === 'unit_admin'" size="small" @click="$router.push(`/unit/${m.unit_id}`)">单位管理</el-button>
              <el-button size="small" type="primary" plain @click="$router.push('/environments')">创建环境</el-button>
            </div>
          </div>
          <div class="quota-bars">
            <div>
              <span class="small muted">CPU 运行中 {{ cpuCores(m.used_cpu_milli) }} / {{ cpuCores(m.cpu_milli) }} 核</span>
              <el-progress :percentage="pct(m.used_cpu_milli, m.cpu_milli)" :stroke-width="6" />
            </div>
            <div>
              <span class="small muted">内存 运行中 {{ memGiB(m.used_mem_mi) }} / {{ memGiB(m.mem_mi) }} GiB</span>
              <el-progress :percentage="pct(m.used_mem_mi, m.mem_mi)" :stroke-width="6" color="#7c3aed" />
            </div>
            <div>
              <span class="small muted">存储 已分配 {{ gi(m.used_storage_gi) }} / {{ gi(m.storage_gi) }} GiB</span>
              <el-progress :percentage="pct(m.used_storage_gi, m.storage_gi)" :stroke-width="6" color="#059669" />
            </div>
          </div>
        </div>
      </div>

      <div class="panel">
        <h3 class="panel-title">最近环境</h3>
        <el-table :data="envs.slice(0, 5)" size="small">
          <el-table-column prop="name" label="名称" min-width="160" />
          <el-table-column prop="unit_name" label="单位" width="150" />
          <el-table-column label="状态" width="110">
            <template #default="{ row }">
              <el-tag size="small" :type="statusOf(row.status).type">{{ statusOf(row.status).label }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="100">
            <template #default>
              <el-button link type="primary" @click="$router.push('/environments')">查看</el-button>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, onMounted, ref } from 'vue'
import api from '@/api'
import { useAuth } from '@/stores/auth'
import { cpuCores, memGiB, gi, pct, statusOf } from '@/utils'

const auth = useAuth()
const loading = ref(false)
const overview = ref({})
const envs = ref([])
const runningCount = computed(() => envs.value.filter((e) => e.status === 'running').length)

async function load() {
  loading.value = true
  try {
    if (auth.isAdmin) {
      overview.value = await api.get('/admin/overview')
    } else {
      await auth.fetchMe()
      envs.value = await api.get('/environments')
    }
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>

<style scoped>
.quick-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: 12px; }
.quick {
  display: flex; gap: 12px; align-items: center; padding: 16px; border: 1px solid #eef2f7;
  border-radius: 12px; cursor: pointer; transition: all .15s;
}
.quick:hover { border-color: #bfdbfe; background: #f8fbff; transform: translateY(-1px); }
.quick .el-icon {
  flex: 0 0 42px; width: 42px; height: 42px; font-size: 22px;
  color: #2563eb; background: #eff6ff; border-radius: 9px;
}
.quick b { display: block; font-size: 14px; }
.quick span { font-size: 12px; color: #6b7280; }
.quota-row { border-bottom: 1px solid #f1f5f9; padding: 14px 0; }
.quota-row:last-child { border-bottom: none; }
.quota-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.quota-bars { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; }
</style>