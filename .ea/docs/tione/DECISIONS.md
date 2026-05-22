# 架构决策与踩坑记录

部署 sub2api 到 TI-ONE 过程中的关键决策和遇到的问题。

## 架构决策

### All-in-One 单镜像

**决策**: 将 PostgreSQL + Redis + sub2api + youtu_proxy 打包到一个镜像,用 supervisord 管理。

**原因**: TI-ONE 在线推理服务只支持单个容器,无法 docker-compose 多容器编排。

**代价**: 镜像 ~660MB;四个进程共享资源;PG 跑在 CFS (网络文件系统) 上性能不如本地盘。

**替代方案 (未选)**:
- 外部托管 PG + Redis (如 TDSQL-C + Redis 云实例) — 更稳定,但额外成本
- SQLite 替代 PG — sub2api 不支持

### PG 16 而非 PG 15

**决策**: 使用 Ubuntu 24.04 自带的 PostgreSQL 16。

**原因**: 从 PGDG 官方仓库 (`apt.postgresql.org`) 拉 PG 15 在开发机 docker build 时下载极慢 (15MB 包卡了 7+ 分钟,最终超时)。Ubuntu 24.04 自带 PG 16,从腾讯云镜像 (`mirrors.tencentyun.com`) 拉取秒级完成。

**兼容性**: sub2api 要求 PG 15+,PG 16 完全兼容。

### CFS 挂载到 `/data/sub2api`

**决策**: CFS 挂载目标是 `/data/sub2api` 而非 TI-ONE 默认的 `/data/model`。

**原因**: TI-ONE 的平台规范保留了 `/data/model` 用于模型文件,自定义服务禁止挂载到该路径 (`mounting to directory '/data/model' is forbidden`)。

**注意**: 镜像 ENV 和 supervisord.conf 中所有路径必须以 `/data/sub2api` 为前缀。CFS 内的子目录名用 `appdata` 而非 `sub2api`,避免 `/data/sub2api/sub2api` 的嵌套混淆。

### youtu_proxy 作为独立进程

**决策**: youtu_llm_proxy.py 以 supervisord 子进程运行在 `127.0.0.1:8088`,sub2api 通过 `AccountTypeUpstream` + `base_url` 调用。

**原因**: 保持 sidecar 语义,youtu_proxy 代码不改动。改动量仅为 sub2api 的一个 commit (`7d267c1d`)。

**替代方案 (未选)**: 用 Go 重写 youtu_proxy 逻辑嵌入 sub2api — 工作量大,与 sidecar 设计初衷相悖。

### 自编译 sub2api

**决策**: 从源码 multi-stage 编译,不用 `FROM weishaw/sub2api:latest`。

**原因**: 项目有大量 `[custom]` 改动 (upstream 支持、request_log 修复、model_mapping 等),官方镜像不包含这些。

## 平台限制

### 服务鉴权

TI-ONE 的 API 调用地址 (`gw.xxx.ti.tencentcs.com`) 默认启用鉴权,要求请求带平台的 `Authorization` token。这与 sub2api 自身的 `x-api-key` 鉴权形成双层认证。

**当前方案**: 关闭 TI-ONE 服务鉴权,仅靠 sub2api 的 API key 认证。

**风险**: 调用地址理论上公网可访问,安全性依赖 sub2api 的 API key。若需加固,可重新开启 TI-ONE 鉴权,但客户端需同时传 `Authorization` (平台 token) 和 `x-api-key` (sub2api key)。

### WebUI 401 问题

TI-ONE WebUI 地址 (`webui.xxx.ti.tencentcs.com`) 的网关行为:
- **POST 请求**: 正常透传所有 header (包括 `Authorization: Bearer <jwt>`)
- **GET 请求**: **剥离 `Authorization` header**

这导致 sub2api 前端登录后,所有 GET API 调用返回 401 (`INVALID_AUTH_HEADER`)。

**证据**: 服务端日志显示 `POST /api/v1/auth/login` → 200,`POST /api/v1/auth/refresh` → 200,但所有 `GET /api/v1/*` → 401。与 TI-ONE 文档 "只支持 POST 方法" 的描述一致。

**解法**: Admin UI 管理通过开发机临时容器 + SSH 隧道完成 (见 README.md 运维章节)。

### API 调用地址不能用于 Web UI

TI-ONE 的 API 网关 (`gw.xxx`) 对静态资源返回错误的 MIME type (把 JS 文件当 `application/json`),导致浏览器拒绝加载前端资源。API 网关仅适用于纯 JSON API 调用。

## 构建踩坑

### docker build 网络

**问题**: 开发机是 K8s Pod,`docker build` 的 bridge 网络无法解析任何外部域名 (npm registry、alpine apk 等)。`docker run` 可以,因为它用主机 DNS,但 build 用默认的 bridge DNS。

**解法**: `docker build --network=host`。

**尝试过但失败的方案**:
- 修改 `/etc/docker/daemon.json` 设置 DNS — 无效,因为 dockerd 不在此 Pod 中
- 只换镜像源 — 域名解析本身就失败,换源无用

### 镜像源配置

开发机 docker build 容器内的网络情况:

| 源 | 可用 | 备注 |
|---|------|------|
| `registry.npmmirror.com` | ✅ | npm / pnpm |
| `mirrors.tencentyun.com` | ✅ | Ubuntu apt |
| `mirrors.tencent.com` | ✅ | Alpine apk, PyPI |
| `goproxy.cn` | ✅ | Go modules |
| `registry.npmjs.org` | ❌ | npm 官方源不通 |
| `dl-cdn.alpinelinux.org` | ❌ | Alpine 官方源不通 |
| `apt.postgresql.org` | ⚠️ | DNS 通但下载极慢 |

### 端口映射

**问题**: 开发机 `docker run -p 18080:8080` 端口映射无效 — docker daemon 在宿主 K8s node 上,不在 Pod 内,端口绑定发生在 node 而非 Pod。

**解法**: `docker run --network=host`,容器直接使用 Pod 的网络栈。

### CFS 数据目录可见性

**问题**: `docker run -v /root/sub2api-prebake:/data/sub2api` 挂载后,Pod 内看不到 `/root/sub2api-prebake/` 的内容 — 因为 bind mount 在 docker daemon host (K8s node) 上,不在 Pod 的文件系统里。

**解法**: 需要通过 docker 容器桥接来操作数据:

```bash
# 把数据从 daemon host 拷到 CFS
docker run --rm \
  -v /root/sub2api-prebake:/src:ro \
  -v /cfs_turbo/easonsshi/sub2api-data:/dst \
  alpine sh -c "cp -a /src/* /dst/"
```

### PG 启动权限

**问题**: initdb 成功但 `pg_ctl start` 失败 — postgres 用户无法写入 logs 目录 (`/data/sub2api/appdata/logs/`)。

**解法**: entrypoint 中 `chmod 777 "${SUB2API_LOG_DIR}"`,让所有子进程都能写入。

### Auto Setup 不触发

**问题**: `AUTO_SETUP=true` + `ADMIN_EMAIL/PASSWORD` 环境变量设了,但 admin 用户没被创建。

**原因**: `NeedsSetup()` 检查 config.yaml 和 install lock 文件是否存在。由于 pgdata 已从 prebake 阶段继承 (CFS 上有数据),config.yaml 存在,`NeedsSetup()` 返回 false,AUTO_SETUP 逻辑被完全跳过。

**解法**: 通过 Python bcrypt + SQL 直接插入 admin 用户。

### youtu_proxy .env 空值

**问题**: 首次启动时 youtu_proxy/.env 是空模板,entrypoint 用 `set -a; . .env; set +a` 把空值 export 到环境。后续改了 .env 内容并 `supervisorctl restart youtu_proxy`,但 youtu_proxy.py 的 `os.environ.setdefault()` 不会覆盖已存在的空字符串。

**解法**: `docker restart` 整个容器 (重新走 entrypoint,从正确的 .env 加载)。

### model_mapping 与 allow_messages_dispatch

**问题**: 请求到 sub2api 但返回 "No available accounts"。

**两个原因**:
1. account 的 `model_mapping` 非空但不包含请求的模型名 → 匹配失败
2. group 的 `allow_messages_dispatch = false` → `/v1/messages` 端点被禁

**解法**:
1. 清空 model_mapping (允许所有模型) 或配置完整映射 + 通配符 `*`
2. `UPDATE groups SET allow_messages_dispatch = true`

**注意**: 修改 DB 后需要 `supervisorctl restart sub2api-stack:sub2api` 清缓存。

### youtu 模型白名单

youtu 内部服务只认识特定模型名。未知模型名返回 403 Forbidden。

当前支持: `claude-opus-4-6`, `claude-sonnet-4-6`

通过 account 的 `model_mapping` 做映射:

| 请求模型 | 映射到 |
|---------|--------|
| `claude-opus-4-6` | `claude-opus-4-6` |
| `claude-opus-4-20250918` | `claude-opus-4-6` |
| `claude-sonnet-4-20250514` | `claude-sonnet-4-6` |
| `claude-sonnet-4-20250618` | `claude-sonnet-4-6` |
| `*` (通配) | `claude-sonnet-4-6` |
