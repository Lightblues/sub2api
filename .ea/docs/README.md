# Sub2API 文档（中文版）

> 部署机器：`ssh anydev_llmrouter`，对外端口 `:80`

Sub2API 是一个 AI API 网关，把多个 Claude / OpenAI / Gemini 订阅账号共享给多个用户，统一处理鉴权、计费、负载均衡。

## 文档目录

| 文件 | 内容 |
|------|------|
| [架构.md](./架构.md) | 技术栈、目录结构、账号体系、路由策略、API 路由、配置项 |
| [api格式转换.md](./api格式转换.md) | Anthropic ↔ OpenAI Responses ↔ ChatCompletions 三向格式转换，含字段映射和 lossy 分析 |
| [请求日志.md](./请求日志.md) | SQLite 请求日志实现：schema、配置、数据格式、已知问题 |
| [inspector.md](./inspector.md) | Web 日志查看器：架构、API、会话识别、前端功能 |
| [运维日志.md](./运维日志.md) | 部署历史、踩坑记录、当前账号/用户状态 |
| [chatcompletions代理.md](./chatcompletions代理.md) | 调研：用 ChatCompletions 后端接 Claude Code，开源方案对比 + 自研设计 |

## 快速参考

### 服务地址

| 服务 | 地址 |
|------|------|
| Sub2API 主服务 | `http://anydev_llmrouter:80` |
| Inspector 日志查看器 | `http://anydev_llmrouter:8019` |
| 内网域名 | `sn5llmrouter.devcloud.woa.com` |
| Youtu LLM Proxy（容器内 sidecar） | `http://127.0.0.1:8088`（仅容器内部） |

### 数据存储位置

| 数据 | 实际路径（宿主机） | 大小（截至 2026-04） |
|------|------------------|-------------------|
| PostgreSQL（服务主数据库） | `/data/docker/lib/volumes/sub2api_sub2api_pg_data/_data/` | ~500 MB |
| SQLite 请求日志（近 7 天） | `/data/docker/lib/volumes/sub2api_sub2api_data/_data/request_logs/` | ~13 GB |
| SQLite 请求日志（归档） | `/apdcephfs/private_easonsshi/data/sub2api_archive/request_logs/` | ~25 GB |
| Redis | `/data/docker/lib/volumes/sub2api_sub2api_redis_data/_data/` | ~25 MB |

> `/data` 盘已扩容至 1TB。请求日志通过 cron 定期归档到 CephFS，详见 [request_log.md](./request_log.md#自动归档)。

### Youtu LLM Proxy（容器内 sidecar）

将腾讯内部 tRPC Claude 服务（`claude-opus/sonnet-4-6`）通过标准 Anthropic Messages API 对外暴露，作为 sub2api 的一个独立计费组（`youtu-claude`, id=20）。

**架构：**
```
用户 → sub2api container :80
         ├─ sub2api (Go, :8080)
         └─ youtu_llm_proxy.py (Python, 127.0.0.1:8088)
              → 内部 tRPC (112.65.194.90:8001)
```

proxy 嵌入 Docker 镜像，由 entrypoint 在 sub2api 启动前后台拉起。通过 `YOUTU_LLM_TOKEN` 环境变量控制是否启动（不配置则不启动，不影响其他部署）。

**脚本位置：** `youtu_llm_proxy.py`（项目根目录，打入镜像 `/app/youtu_llm_proxy.py`）

**配置：** `.env` 中的 `YOUTU_LLM_*` 变量，通过 `docker-compose.yml` 透传到容器

**日志：** 容器内 `/app/data/youtu_proxy.log`（挂载到 Docker volume `sub2api_data`）

```bash
# 健康检查（容器内）
docker exec sub2api wget -q -O - http://127.0.0.1:8088/health

# 查看 proxy 日志
docker exec sub2api cat /app/data/youtu_proxy.log

# 查看容器内进程
docker exec sub2api ps aux
```

**sub2api 账号（Admin UI）：**

| 项 | 值 |
|----|-----|
| Group | `youtu-claude`（id=20，platform=anthropic） |
| Account | `youtu-llm-proxy`（id=400，type=upstream，base_url=`http://127.0.0.1:8088`） |

**Go 代码修改（`[custom]` commits）：**
- `backend/internal/service/account.go`：`GetBaseURL()` 支持 `AccountTypeUpstream`
- `backend/internal/service/gateway_service.go`：`GetAccessToken()` 和 `buildUpstreamRequest()` 支持 `AccountTypeUpstream`

### 重建部署

```bash
ssh anydev_llmrouter
cd /root/sub2api && git pull origin eason
docker build -t sub2api:eason .
docker compose up -d sub2api
```

### 重启 Inspector

**进程管理：** systemd（`sub2api-inspector.service`），自动重启 + 开机自启

环境变量 `INSPECTOR_ARCHIVE_DIR` 指向 CephFS 归档目录，viewer 透明读取归档数据。

```bash
# 状态/重启/日志
systemctl status sub2api-inspector
systemctl restart sub2api-inspector
tail -f /root/sub2api_inspector.log

# 更新代码后重启
cd ~/ea-fork && git pull origin main
uv pip install -e packages/sub2api-inspector/
systemctl restart sub2api-inspector
```
