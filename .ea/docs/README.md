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
| Youtu LLM Proxy（sidecar） | `http://anydev_llmrouter:8088`（仅内网/宿主机） |

### 数据存储位置

| 数据 | 实际路径（宿主机） | 大小（截至 2026-04） |
|------|------------------|-------------------|
| PostgreSQL（服务主数据库） | `/data/docker/lib/volumes/sub2api_sub2api_pg_data/_data/` | ~500 MB |
| SQLite 请求日志 | `/data/docker/lib/volumes/sub2api_sub2api_data/_data/request_logs/` | ~21 GB（持续增长） |
| Redis | `/data/docker/lib/volumes/sub2api_sub2api_redis_data/_data/` | ~25 MB |

> ⚠️ SQLite 日志体积大，是 `/data` 盘（98G）的主要增长来源。**待办**：迁移到 `/mnt/private/`（2TB ceph）。

### Youtu LLM Proxy（方案 C sidecar）

将腾讯内部 tRPC Claude 服务（`claude-opus/sonnet-4-6`）通过标准 Anthropic Messages API 对外暴露，作为 sub2api 的一个独立计费组（`youtu-claude`, id=20）。

**架构：**
```
sub2api (Docker :80)  →  youtu_llm_proxy.py (宿主机 :8088)  →  内部 tRPC (112.65.194.90:8001)
                  http://172.21.0.1:8088
```

**配置文件：** `.ea/.env`（含 `YOUTU_LLM_TOKEN` 等敏感凭证，已被 `.gitignore` 排除）

**脚本位置：** `.ea/youtu_llm_proxy.py`（同步到服务器 `/root/youtu_llm_proxy.py`）

**启动/重启 Proxy：**
```bash
ssh anydev_llmrouter
# 复制最新脚本
scp .ea/youtu_llm_proxy.py anydev_llmrouter:/root/youtu_llm_proxy.py

# 重启
pkill -f youtu_llm_proxy
nohup python3 /root/youtu_llm_proxy.py --env /root/youtu_llm_proxy.env --host 0.0.0.0 \
    >/root/youtu_llm_proxy.log 2>&1 </dev/null &

# 检查
curl http://127.0.0.1:8088/health
tail -f /root/youtu_llm_proxy.log
```

**sub2api 账号（Admin UI）：**

| 项 | 值 |
|----|-----|
| Group | `youtu-claude`（id=20，platform=anthropic） |
| Account | `youtu-llm-proxy`（id=400，type=upstream，base_url=`http://172.21.0.1:8088`） |

> `172.21.0.1` 是 Docker bridge `sub2api-network` 的网关 IP（即宿主机地址），容器通过此 IP 访问宿主机上的 proxy。

**Go 代码修改（`[custom]` commits）：**
- `backend/internal/service/account.go`：`GetBaseURL()` 支持 `AccountTypeUpstream`
- `backend/internal/service/gateway_service.go`：`GetAccessToken()` 和 `buildUpstreamRequest()` 支持 `AccountTypeUpstream`

### 重建部署

```bash
ssh anydev_llmrouter
cd /root/sub2api/src && git pull origin eason
docker build -t sub2api:eason .
cd /root/sub2api && docker compose up -d sub2api
```

### 重启 Inspector

```bash
ssh anydev_llmrouter
cd ~/ea-fork && git pull origin main
pkill -f sub2api-inspector
nohup .venv/bin/sub2api-inspector > /var/log/sub2api-inspector.log 2>&1 &
```
