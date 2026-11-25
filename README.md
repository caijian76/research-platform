# 科研云平台（Research Platform）

面向科研人员的容器化科研环境平台。用户通过浏览器登录后，可以按二级单位分配的资源额度创建 RootPV（持久化根文件系统）容器环境，并通过 **SSH / WebSSH / noVNC 图形桌面** 使用。

- 前端：Vue 3 + Vite + Element Plus + Pinia + xterm.js
- 后端：Go 1.24 + 标准库 `net/http` + `client-go`
- 数据库：PostgreSQL 16
- 运行环境：Kubernetes（RootPV 依赖 OpenFuyao RootPV 能力）

---

## 功能特性

- **多级管理**
  - 平台管理员：管理集群总资源、二级单位及其资源上限、平台用户、容器模板、平台设置；「科研环境」位于「平台管理」分组，仅用于查看与运维平台内全部环境，不提供新建环境功能。
  - 二级管理员：管理本单位成员，并为成员分配个人资源上限。
  - 科研人员：在所属单位额度内创建、使用科研环境。
- **按单位资源分配**
  - 单位额度受平台总资源约束，同时在集群中落为独立的 `ResourceQuota` / `LimitRange`。
  - CPU / 内存额度约束的是**运行中 Pod 的 requests 总和**，由命名空间 `ResourceQuota` 强制执行。
  - 存储额度采用**不可超分**分配：单位存储上限必须覆盖其全部成员存储配额之和；成员的存储配额必须覆盖其全部环境配置存储之和，包含已停止的环境。
  - 成员额度口径相同：单个环境不超过个人 CPU / 内存上限，运行中环境合计不超过个人上限，全部环境存储合计不超过个人存储上限。
  - 单位的"已分配存储"展示成员存储配额合计，同时显示环境实际占用；成员的"已分配存储"展示其全部环境配置存储之和。停止环境立即释放运行中 CPU/内存，删除环境释放环境存储占用。
- **容器模板管理**
  - 配置镜像、默认 CPU/内存/存储、SSH 容器端口、noVNC 容器端口、是否 RootPV、是否启用。
- **科研环境**
  - 支持启动、停止、重启、删除；RootPV 环境删除重建后数据保留。
  - root 密码 / VNC 密码留空时自动生成（root 14 位、VNC 8 位）。
  - 可选"开启图形化界面"：开启时容器启动 `sshd,vnc,websockify` 并提供 noVNC；关闭时仅启动 `sshd`。
- **连接方式**
  - SSH：显示主机、NodePort、用户名、root 密码及完整 SSH 命令。
  - WebSSH：浏览器内 xterm 终端，**在新标签页以全屏页面打开**。
  - noVNC：新标签页打开图形桌面，也可在连接对话框内嵌显示。
- **端口映射**
  - 环境列表「更多 → 端口」打开端口映射窗口，集中显示 SSH、图形桌面与自定义端口。
  - 可添加自定义端口映射：指定容器内端口与协议（TCP/UDP），由 Kubernetes 自动分配 NodePort（不可手动指定），外部通过 `NodeIP:NodePort` 访问。
  - SSH 与图形桌面端口为系统端口，不可删除；每个环境最多添加 5 个自定义端口映射（不占用系统端口名额）。
  - 环境列表「端口映射」列以「类型 / 容器内端口 / NodePort」统一展示 SSH、图形桌面与自定义端口。
- **命名空间隔离**
  - 平台自身组件运行在 `research-system`。
  - 每个二级单位的环境运行在独立命名空间 `research-unit-<slug>`。
- **审计**
  - 登录、用户/单位/模板管理、环境操作、快照管理等写入 `audit_logs`。
  - 平台管理员可在「平台管理 → 审计日志」中按操作者、操作类型、关键字和时间范围查询，导出 CSV，并可一键删除 7 天以前的日志（先预览条数并二次确认）。

---

## 架构

```text
                       ┌──────────────────────────────────────────┐
 Browser ── HTTP ─────▶ │  research-platform (Go + Vue SPA)        │
   :30080               │  REST API + WebSSH WebSocket + 静态资源   │
                       └───────┬──────────────────────┬───────────┘
                               │                      │
                               ▼                      ▼
                        PostgreSQL 16           Kubernetes API
                        (平台元数据)            (Pod/Service/PVC/Quota)
                                                      Kubernetes 资源分布：
                       research-system          平台自身（后端、PostgreSQL）
                       research-unit-<slug>     单位隔离命名空间
                         ├─ Pod (allinone 镜像, RootPV annotations)
                         ├─ PVC (RootPV 持久化根文件系统, rook-ceph-block)
                         ├─ Service ssh    22 → Pod:22（NodePort 自动分配）
                         └─ Service novnc  NodePort → 6080 → Pod:<vnc_port>
```

连接链路：

| 方式 | 链路 |
|---|---|
| SSH | 浏览器/终端 → `NodeIP:<ssh NodePort>` → Pod:22 |
| WebSSH | 浏览器 WebSocket → `/api/environments/{id}/webssh` → 后端通过集群内 DNS 连接 `<pod>-ssh:22` |
| noVNC | 浏览器 → `NodeIP:<vnc NodePort>` → Service:6080 → Pod:6901（websockify） |
| 自定义端口 | 客户端 → `NodeIP:<自动分配 NodePort>` → Service → Pod:<容器端口>（TCP/UDP） |

---

## 目录结构

```text
research-platform/
├── backend/                     # Go 后端
│   ├── cmd/server/main.go       # 入口：配置、迁移、种子数据、HTTP 服务
│   ├── internal/
│   │   ├── api/                 # 路由、认证中间件、环境/单位/模板/用户/WebSSH
│   │   ├── auth/                # JWT、bcrypt、AES-GCM 加解密
│   │   ├── config/              # 环境变量配置
│   │   ├── db/                  # PostgreSQL 访问与表结构
│   │   ├── kube/                # client-go：NS、Quota、PVC、Pod、Service、状态同步
│   │   └── models/              # 数据模型
│   └── web/                     # 前端构建产物（Vite 输出，后端静态托管）
├── frontend/                    # Vue 3 前端
│   └── src/
│       ├── views/               # 工作台、科研环境、单位、用户、模板、WebSSH、审计日志等
│       ├── components/          # 布局、连接对话框
│       ├── router/              # 路由与登录守卫
│       └── stores/              # Pinia 认证状态
├── images/
│   ├── backend/Containerfile    # 后端镜像
│   └── workspace/               # 可选示例工作区镜像（默认构建脚本未使用）
├── deploy/                      # Kubernetes 清单
│   ├── 00-namespace-rbac.yaml
│   ├── 01-postgres.yaml
│   ├── 02-secrets-config.yaml
│   └── 03-backend.yaml
├── scripts/
│   ├── build.sh                 # 构建前后端、镜像并导入 containerd
│   └── deploy.sh                # 应用清单并等待 rollout
└── dist/                        # 构建输出（生成）
```

---

## 快速开始

### 前置条件

- Kubernetes 集群（验证环境：v1.33 单节点）与可用的动态存储类（默认 `rook-ceph-block`）。
- 若使用 RootPV 模板：
  - 集群具备 OpenFuyao RootPV 能力；
  - 至少一个节点带有标签 `openfuyao.cn/persistent-rootfs=true`；
  - Pod 会使用 `nodeSelector` 调度到该节点。
- 构建机/节点：
  - Go 1.24+、Node.js 18+、npm、`make`、`podman`、`kubectl`、`ctr`（需能操作 `k8s.io` containerd 命名空间）。
- 网络：
  - 可拉取 `docker.1ms.run/library/postgres:16-alpine` 与工作区镜像；
  - 集群节点可拉取工作区镜像（默认 `registry.cn-hangzhou.aliyuncs.com/jackc/ubuntu-24-allinone:v0.4.0`）。

### 构建镜像

```bash
cd /root/research-platform
bash scripts/build.sh
```

也可使用 Makefile（`make help` 查看全部目标）：

```bash
make build     # 等价于 scripts/build.sh
make deploy    # 等价于 scripts/deploy.sh
make release   # 构建并部署
```

脚本会：

1. 构建 Vue 前端到 `backend/web`；
2. 编译 Go 后端（静态二进制）；
3. 构建后端镜像 `research-platform-backend:1.0.0`；
4. 拉取 PostgreSQL 与工作区镜像；
5. 通过 `podman save` + `ctr images import` 导入 Kubernetes containerd，并补打 `docker.io/library/...` 标签。

> 构建过程使用 `TMPDIR=/var/tmp`，避免根分区空间不足。

### 部署

```bash
cd /root/research-platform
bash scripts/deploy.sh
```

脚本会应用 `deploy/` 下清单、将 ConfigMap 的 `PUBLIC_HOST` 自动设置为节点 InternalIP，并等待 PostgreSQL 与后端 rollout 完成。

访问地址：

```text
http://<NodeIP>:30080
```

默认平台管理员（仅首次初始化空库时创建）：

```text
用户名：admin
密码：  Admin@12345
```

> 生产环境请立即修改 `deploy/02-secrets-config.yaml` 中的 `ADMIN_PASSWORD`、`JWT_SECRET`、`SECRET_KEY`、`POSTGRES_PASSWORD` 后再首次部署，并在平台内修改管理员密码。

### 冒烟测试

1. 平台管理员登录 → 平台设置：确认集群总资源上限。
2. 二级单位：创建单位（slug 会自动生成命名空间 `research-unit-<slug>`）。
3. 单位详情：添加二级管理员 / 科研人员，并分配个人资源上限。
4. 容器模板：确认/创建模板（SSH 端口 22，noVNC 端口 6901）。
5. 科研环境：新建环境（可关闭图形化），等待运行中。
6. 点击"连接"：
   - SSH：复制命令登录；
   - WebSSH：新标签页打开终端；
   - noVNC（图形化环境）：新标签页打开桌面。
7. 停止/启动环境，验证 RootPV 数据保留。

---

## 配置

### ConfigMap（`research-platform-config`）

| 变量 | 默认值 | 说明 |
|---|---|---|
| `PUBLIC_HOST` | 空（自动获取节点 IP） | 连接信息中展示的主机地址；`deploy.sh` 会自动写入节点 InternalIP |
| `STORAGE_CLASS` | `rook-ceph-block` | RootPV 的 PVC 存储类 |
| `SEED_TEMPLATE_IMAGE` | allinone 镜像 | 空库首次启动时创建种子模板使用的镜像 |
| `ADMIN_USERNAME` | `admin` | 首次初始化创建的平台管理员用户名 |
| `UNIT_NS_PREFIX` | `research-unit-` | 二级单位命名空间前缀（后端会拒绝删除非该前缀的 NS） |
| `PLATFORM_STORAGE_GI` | `60` | 平台存储总资源的兜底值（平台设置为空时使用） |

### Secret（`research-platform-secrets`）

| 变量 | 说明 |
|---|---|
| `POSTGRES_PASSWORD` | PostgreSQL 密码 |
| `JWT_SECRET` | 登录 JWT 签名密钥（令牌有效期 24 小时） |
| `SECRET_KEY` | AES-GCM 密钥，用于加密环境 root/VNC 密码。**更换后旧密码无法解密** |
| `ADMIN_PASSWORD` | 首次初始化管理员密码（已有用户时不会覆盖） |

### 后端内置环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址 |
| `DATABASE_URL` | `postgres://research:research@localhost:5432/research?sslmode=disable` | PostgreSQL 连接串 |
| `WEB_DIR` | `/app/web` | SPA 静态资源目录 |

### 配置修改后的生效方式

```bash
kubectl -n research-system apply -f deploy/02-secrets-config.yaml
kubectl -n research-system rollout restart deployment/research-platform
```

---

## 使用说明

### 多级管理模型

```text
平台管理员
  └─ 二级单位（资源上限 + 命名空间 + ResourceQuota）
       ├─ 二级管理员（成员管理 + 成员资源上限）
       └─ 科研人员（创建/使用自己的环境）
```

- 平台管理员创建单位时，单位额度不能超过平台总资源减去已分配给其他单位的额度。
- 二级管理员只能管理自己所属单位；平台用户管理页会以只读方式显示用户所属二级单位。
- 二级管理员的「工作台」和「科研环境」只展示其个人环境与个人配额；单位内的全部成员、环境与资源使用情况统一在「单位管理」中查看。
- 平台管理员的「科研环境」位于侧边栏「平台管理」分组，展示平台内全部环境，仅可查看、启动、停止、重启、编辑与删除；平台管理员不能新建环境，新建请由二级单位成员操作（后端同样拒绝平台管理员创建请求）。
- 环境只能创建在用户所属单位的命名空间内。

### 资源额度口径

- **CPU / 内存（运行中）**：统计 `desired_state=running` 的环境，即当前应当存在 Pod 的环境；停止环境后立即从已用额度中释放。单位级同时由命名空间 `ResourceQuota` 对运行中 Pod 的 requests 强制约束。
- **存储（用户已分配）**：统计该用户全部环境的配置存储之和，**包含已停止的环境**；只有删除环境才会释放。
- **单位存储分配（不可超分）**：单位"已分配存储"按该单位全部成员的存储配额之和计算，不能超过单位存储上限；成员存储配额之和也不能超过单位存储上限。
- **单位额度**：单位内全部运行中 Pod 的 CPU/内存请求不能超过单位上限；单位内全部成员的存储配额合计不能超过单位存储上限，且全部环境实际存储也不能超过该上限。
- **成员额度**：
  - 单个环境的 CPU / 内存请求不能超过该成员的 CPU / 内存上限；
  - 该成员全部环境的存储配置合计不能超过其存储上限（创建时校验，包含已停止环境）；
  - 该成员所有运行中环境的 CPU / 内存请求合计不能超过其上限（仅在启动/重启时校验）；
  - 新增或调整成员存储配额时，其余成员存储配额与本次配额之和不能超过单位存储上限。
- 因此成员可以创建多个环境，只要每个环境的 CPU/内存不超过个人上限、全部环境存储合计不超过个人存储上限即可（例如配额 2C4G/10G 可创建 5 个 2C4G/2G 环境）；同一时刻只能运行其中资源之和不超过个人与单位额度的部分。新建环境默认不启动，启动时才校验运行中 CPU/内存额度。单位存储上限不能调整到低于成员存储配额合计或环境实际存储总量；个人或单位 CPU/内存下限不能调整到低于当前运行中用量。

### 新建科研环境

- **所属单位 / 容器模板**：决定命名空间、镜像和默认规格；表单会按模板默认值与个人静态配额自动钳制，存储默认值同时受剩余存储额度约束。
- **root 密码**：6–64 位；留空自动生成 14 位随机密码，通过 `ROOT_PW` 注入。
- **图形化界面**：
  - 开启（默认）：显示 VNC 密码输入框；留空自动生成 8 位密码；容器 `SERVICES=sshd,vnc,websockify`。
  - 关闭：不显示 VNC 密码；`SERVICES=sshd`；不创建 noVNC Service/NodePort。
- **规格**：创建时只校验单个环境的 CPU/内存不超过个人上限、全部环境存储合计不超过个人与单位上限；不考虑当前运行中 Pod 的 CPU/内存用量。启动时才校验运行中合计是否超过个人与单位上限。
- **大SHM**：内存配置右侧可开启「大shm」并填写 MiB 大小（不能超过内存的 1/2）。开启后创建 Pod 时会挂载 `medium: Memory` 的 emptyDir 到 `/dev/shm`，`sizeLimit` 为填写值；写入 SHM 的内容占用容器内存，过大可能导致 OOM。
- **默认状态**：创建后环境为「已停止」，仅创建 RootPV PVC（如适用）与数据库记录，不创建 Pod/Service；需在列表中点击「启动」后连接使用。
- 每次打开新建对话框都会重新拉取额度，新建/停止/启动/删除后"运行中 CPU/内存"和"已分配存储"会实时更新。

### 编辑科研环境

- 仅当环境处于「已停止」且 Pod 已关闭时可编辑；运行中、启动中、异常状态不显示编辑入口，后端也会拒绝。
- 编辑表单与新建一致，但不显示「所属单位」和「容器模板」，二者不可修改。
- root / VNC 密码留空表示保持原密码；填写新值后仅对下次启动的容器生效。若原本未开启图形界面，编辑时开启且 VNC 密码留空会自动生成新密码。
- CPU / 内存可按个人静态配额调整，启动时仍会校验运行中资源合计。
- 存储只能增加、不能减少；调整后重新统计个人与单位的环境存储总量并校验额度。存储增加时后端会对 RootPV PVC 执行扩容，文件系统扩容在下次启动挂载时完成。
- 关闭图形化界面会删除 noVNC Service；重新开启后下次启动自动重建。
- 「大shm」可开关并调整大小（不超过内存的 1/2），保存后下次启动重建 Pod 时生效。

### 环境生命周期

| 操作 | 行为 |
|---|---|
| 创建 | 仅创建数据库记录与 RootPV PVC（如有），默认停止，不创建 Pod/Service |
| 编辑 | 仅停止状态可用；更新名称/规格/密码/图形化/大SHM，存储只增不减并扩容 PVC |
| 停止 | 删除 Pod（保留数据库记录、Service 与 RootPV PVC） |
| 启动 | 确保 Pod/Service 存在，复用已有 PVC，并完成待处理的文件系统扩容 |
| 重启 | 删除并重建 Pod，等待 RootPV 租约释放后重新创建 |
| 删除 | 删除 Pod/Service/PVC 与数据库记录（RootPV 数据不可恢复） |

---

## 容器与镜像要求

平台按模板注入以下环境变量（工作区镜像需自行消费）：

| 变量 | 说明 |
|---|---|
| `ROOT_PW` | root 密码，用于 SSH / WebSSH |
| `VNC_PW` | VNC 密码（仅图形化环境） |
| `VNC_GEOMETRY` | 图形分辨率，默认 `1440x900` |
| `SERVICES` | 需要启动的服务：`sshd` 或 `sshd,vnc,websockify` |
| `WORKSPACE_NAME` | 环境名称 |

镜像约定：

- SSH 监听容器端口 **22**。
- noVNC/websockify 监听模板配置的 `vnc_port`（默认 **6901**）。
- 平台创建的 noVNC Service：`NodePort → Service:6080 → Pod:<vnc_port>`。
- 仅需 SSH 的环境可以关闭图形化，此时不会要求镜像启动 VNC。

测试镜像：

```text
registry.cn-hangzhou.aliyuncs.com/jackc/ubuntu-24-allinone:v0.4.0
```

## RootPV 持久化

RootPV 环境会：

1. 创建 PVC（`storageClass=STORAGE_CLASS`，大小取环境存储配额）；
2. 为 Pod 添加以下注解：
   - `openfuyao.cn/persistent-rootfs=true`
   - `openfuyao.cn/rootfs-container=workspace`
   - `openfuyao.cn/rootfs-volume=rootfs-data`
   - `openfuyao.cn/rootfs-path=containers/<pod-name>`
   - `openfuyao.cn/rootfs-base-image-policy=Strict`
3. 由 RootPV 控制器依据上述注解，将 PVC 内容作为容器的持久化根文件系统；
4. RootPV 环境同时设置 `nodeSelector: openfuyao.cn/persistent-rootfs=true`，调度到支持 RootPV 的节点。

临时环境不创建 PVC，Pod 删除后数据丢失。

---

## 数据模型（PostgreSQL）

数据库名 `research`，启动时自动建表/迁移（`CREATE TABLE IF NOT EXISTS` + `ALTER TABLE ... IF NOT EXISTS`）。

| 表 | 内容 |
|---|---|
| `users` | 平台账号、bcrypt 密码哈希、平台管理员标志、禁用标志 |
| `units` | 二级单位、namespace、CPU/内存/存储上限 |
| `memberships` | 用户-单位关系、角色（`unit_admin` / `member`）、个人资源上限 |
| `templates` | 容器模板：镜像、RootPV 标志、默认规格、SSH/noVNC 端口、启用状态 |
| `environments` | 环境元数据：Pod/PVC/Service 名称、资源、端口、加密密码、期望状态与运行状态 |
| `env_ports` | 自定义端口映射：容器端口、自动分配的 NodePort、Service 名称与协议 |
| `snapshots` | 环境快照记录：名称、K8s VolumeSnapshot 名称、状态、大小与错误信息 |
| `settings` | 平台集群总资源上限（单行） |
| `audit_logs` | 操作审计日志（用户、动作、目标、详情、时间） |

说明：

- 环境的 root/VNC 密码使用 AES-GCM 加密存储，需要 `SECRET_KEY` 才能解密。
- Kubernetes 中的 Pod/Service 状态由集群维护，数据库中的状态与 NodePort 为同步结果。
- 容器内科研数据保存在 PVC 中，不写入 PostgreSQL。

备份数据库：

```bash
kubectl -n research-system exec deploy/postgres -- \
  pg_dump -U research research | gzip > research-db-$(date +%F).sql.gz
```

---

## API 概览

所有接口以 `/api` 为前缀；除登录与健康检查外均需 `Authorization: Bearer <JWT>`。WebSSH 因浏览器 WebSocket 无法自定义请求头，使用 `?token=<JWT>`。

| 分组 | 接口 |
|---|---|
| 认证 | `POST /auth/login`、`GET /me`、`POST /me/password` |
| 模板 | `GET /templates` |
| 环境 | `GET/POST /environments`、`GET/DELETE /environments/{id}`、`POST /environments/{id}/stop|start|restart`、`GET /environments/{id}/webssh` |
| 单位 | `GET /units/{id}`、`GET/POST /units/{id}/members`、`PATCH/DELETE /units/{id}/members/{uid}`、`GET /units/{id}/environments` |
| 平台管理 | `GET /admin/overview`、`GET/PUT /admin/settings`、`GET/POST /admin/units`、`PATCH/DELETE /admin/units/{id}`、`GET/POST /admin/users`、`PATCH/DELETE /admin/users/{id}`、`GET/POST /admin/templates`、`PATCH/DELETE /admin/templates/{id}`、`GET /admin/environments`、`GET /admin/audit-logs`、`DELETE /admin/audit-logs?days=7` |
| 快照 | `GET/POST /environments/{id}/snapshots`、`DELETE /environments/{id}/snapshots/{sid}`、`POST /environments/{id}/snapshots/{sid}/restore` |
| 端口映射 | `GET/POST /environments/{id}/ports`、`DELETE /environments/{id}/ports/{pid}` |
| 健康检查 | `GET /api/healthz` |

> `DELETE /api/admin/audit-logs` 默认清理 7 天以前的日志，可用 `days` 指定 1–3650 天；带 `dry_run=1` 时只返回待删除条数，不执行删除。

---

## 本地开发

```bash
# 1. 准备 PostgreSQL（或使用集群中的 postgres 做端口转发）
kubectl -n research-system port-forward svc/postgres 5432:5432

# 2. 启动后端
cd backend
# 按实际数据库密码填写；密码含特殊字符时需 URL 编码
export DATABASE_URL='postgres://research:<password>@127.0.0.1:5432/research?sslmode=disable'
export KUBECONFIG=/root/.kube/config
export SECRET_KEY='change-me-research-platform-encryption-2026'
export JWT_SECRET='change-me-research-platform-jwt-2026'
export PUBLIC_HOST=127.0.0.1
go run ./cmd/server

# 3. 启动前端开发服务器（/api 代理到 127.0.0.1:8080）
cd frontend
npm install
npm run dev
```

前端生产构建输出到 `backend/web`，由后端统一托管。

---

## 运维

```bash
# 查看后端日志
kubectl -n research-system logs deploy/research-platform -f

# 查看数据库
kubectl -n research-system exec -it deploy/postgres -- \
  psql -U research -d research

# 更新代码后重新构建并部署
bash scripts/build.sh
bash scripts/deploy.sh

# 仅更新后端二进制/前端产物后滚动重启
kubectl -n research-system rollout restart deployment/research-platform
kubectl -n research-system rollout status deployment/research-platform
```

删除单位前必须先删除该单位下所有环境；成功后平台会删除对应的单位命名空间。

---

## 安全说明

- 部署前务必替换 `deploy/` 中的默认 Secret（数据库密码、JWT 密钥、加密密钥、管理员密码）。
- 登录密码使用 bcrypt 存储；JWT 有效期 24 小时。
- 环境 root/VNC 密码使用 AES-GCM 加密存储；`SECRET_KEY` 变更会导致已存密码无法解密。
- noVNC 完整地址中带 VNC 密码查询参数，便于一键打开；请仅在可信环境中分享。
- WebSSH 校验 JWT 与环境访问权限，并限制 WebSocket 同源连接。
- 当前通过 NodePort 直接暴露服务，未内置 TLS。生产环境建议增加 Ingress/LoadBalancer 与 HTTPS。
- 审计日志记录了关键管理操作；平台管理员可通过「审计日志」页面或 `GET /api/admin/audit-logs` 查询，并导出 CSV。

---

## 已知限制

- 后端为单副本无状态服务；数据库为单实例 PostgreSQL（`Recreate` 策略），未实现高可用。
- 环境通过 NodePort 暴露，依赖节点网络可达；未集成 Ingress/域名访问。
- RootPV 依赖 OpenFuyao RootPV 能力与带标签节点；普通集群只能使用临时环境。
- 未提供镜像仓库凭据（imagePullSecrets）配置，私有镜像需预先配置节点凭据。
- VNC 协议密码最多 8 位，平台会限制并截断到 8 位。
- 图形化环境要求工作区镜像按 `SERVICES` 启动 `sshd,vnc,websockify`，且 websockify 端口与模板 `vnc_port` 一致。