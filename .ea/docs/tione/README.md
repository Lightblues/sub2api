# Sub2API on TI-ONE

将 sub2api (含 youtu_llm_proxy sidecar) 部署到腾讯 TI-ONE 在线推理服务平台。

## 架构

```
┌──────────────────── TI-ONE 容器 ────────────────────┐
│  tini (PID 1)                                        │
│   └─ entrypoint.sh                                   │
│       └─ supervisord                                  │
│           ├─ [10] postgres    127.0.0.1:5432          │
│           ├─ [20] redis       127.0.0.1:6379          │
│           ├─ [25] youtu_proxy 127.0.0.1:8088          │
│           └─ [30] sub2api    *:8080  ← TI-ONE 暴露   │
│                                                       │
│  镜像内 (不会被 CFS 覆盖):                            │
│   /app/sub2api          Go 二进制                     │
│   /opt/youtu_proxy/     Python sidecar                │
│                                                       │
│  CFS 挂载: /data/sub2api/                             │
│   ├── pgdata/           PostgreSQL 数据               │
│   ├── redis/            Redis AOF                     │
│   ├── appdata/                                        │
│   │   ├── config.yaml   sub2api 配置                  │
│   │   ├── .db_password  PG 密码 (自动生成)            │
│   │   └── logs/         所有进程日志                   │
│   └── youtu_proxy/                                    │
│       └── .env          youtu 凭证                    │
└───────────────────────────────────────────────────────┘
```

请求链路:

```
Claude Code → sub2api (:8080) → youtu_proxy (:8088) → 腾讯内部 tRPC LLM 服务
```

## 快速开始

### 前提

- 开发机 `ssh tione_llmrouter` 可连且有 docker
- CFS `/cfs_turbo/easonsshi/` 已挂载
- 已 `docker login zwccr.ccs.tencentyun.com`

### 1. 构建镜像

```bash
# 在开发机上
cd /root/sub2api
docker build --network=host \
  -f tione/Dockerfile.tione \
  -t zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base:sub2api-v3 \
  .
```

> `--network=host` 是必须的。开发机是 K8s Pod, docker build 的 bridge 网络 DNS 解析不通。详见 [DECISIONS.md](DECISIONS.md#docker-build-网络)。

### 2. 预烘 (Prebake)

首次部署需在开发机上运行一次容器,完成数据库初始化和 Admin 配置:

```bash
# 准备 CFS 目录
CFS=/cfs_turbo/easonsshi/sub2api-data

# 起临时容器 (--network=host 避免端口映射问题)
docker run -d --name sub2api-prebake --network=host \
  -v $CFS:/data/sub2api \
  -e TZ=Asia/Shanghai \
  zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base:sub2api-v3

# 等 ~15s 直到全部进程 RUNNING
docker logs -f sub2api-prebake
# 看到 "success: sub2api entered RUNNING state" 即可 Ctrl-C
```

#### 2a. 创建 Admin 用户

首次启动时 users 表为空。通过 SQL 创建:

```bash
# 生成 bcrypt hash
HASH=$(docker exec sub2api-prebake python3 -c "
import subprocess, sys
subprocess.check_call([sys.executable, '-m', 'pip', 'install', '--break-system-packages', 'bcrypt', '-q'], stderr=subprocess.DEVNULL)
import bcrypt
print(bcrypt.hashpw(b'YOUR_PASSWORD', bcrypt.gensalt()).decode())
")

# 插入 admin
docker exec sub2api-prebake bash -c "
PGPASSWORD=\$(cat /data/sub2api/appdata/.db_password) psql -h 127.0.0.1 -U sub2api -d sub2api -c \"
INSERT INTO users (email, password_hash, role, balance, concurrency, status, created_at, updated_at, username)
VALUES ('admin@sub2api.local', '\$HASH', 'admin', 0, 10, 'active', NOW(), NOW(), 'admin');
\""
```

#### 2b. 配置 Upstream 账号

通过 SSH 隧道 (`ssh -L 8080:127.0.0.1:8080 tione_llmrouter`) 访问 `http://localhost:8080` 的 Admin UI,添加:

- **账号类型**: `upstream`
- **Base URL**: `http://127.0.0.1:8088`
- **API Key**: `dummy`

或直接 SQL:

```bash
docker exec sub2api-prebake bash -c "
PGPASSWORD=\$(cat /data/sub2api/appdata/.db_password) psql -h 127.0.0.1 -U sub2api -d sub2api -c \"
UPDATE accounts SET type='upstream', credentials='{
  \\\"api_key\\\": \\\"dummy\\\",
  \\\"base_url\\\": \\\"http://127.0.0.1:8088\\\",
  \\\"model_mapping\\\": {
    \\\"claude-opus-4-6\\\": \\\"claude-opus-4-6\\\",
    \\\"claude-opus-4-20250918\\\": \\\"claude-opus-4-6\\\",
    \\\"claude-sonnet-4-6\\\": \\\"claude-sonnet-4-6\\\",
    \\\"claude-sonnet-4-20250514\\\": \\\"claude-sonnet-4-6\\\",
    \\\"claude-sonnet-4-20250618\\\": \\\"claude-sonnet-4-6\\\",
    \\\"*\\\": \\\"claude-sonnet-4-6\\\"
  }
}' WHERE id=1;

UPDATE groups SET allow_messages_dispatch=true WHERE id=1;
\""
```

#### 2c. 写入 youtu 凭证

```bash
docker exec sub2api-prebake bash -c 'echo -e "YOUTU_LLM_BASE=http://112.65.194.90:8001\nYOUTU_LLM_USERNAME=<user>\nYOUTU_LLM_USERID=<uid>\nYOUTU_LLM_TOKEN=<token>\nYOUTU_LLM_PROXY_PORT=8088" > /data/sub2api/youtu_proxy/.env'

# 重启容器让 entrypoint 重新加载 .env
docker restart sub2api-prebake
```

#### 2d. 验证

```bash
docker exec sub2api-prebake curl -sS http://127.0.0.1:8080/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: <your-api-key>" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-sonnet-4-20250514","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}'
```

#### 2e. 停止预烘容器

```bash
docker rm -f sub2api-prebake
```

### 3. 推镜像

```bash
docker push zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base:sub2api-v3
```

### 4. TI-ONE 控制台配置

| 配置项 | 值 |
|--------|-----|
| 镜像 | `zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base:sub2api-v3` |
| 容器端口 | `8080` |
| CFS 挂载源 | `/cfs_turbo/easonsshi/sub2api-data` |
| CFS 挂载目标 | `/data/sub2api` |
| 健康检查 | `GET /health` 端口 `8080` |
| 服务鉴权 | **关闭** (详见 [DECISIONS.md](DECISIONS.md#服务鉴权)) |

### 5. 使用

TI-ONE 会提供两个地址:

| 地址类型 | 用途 |
|---------|------|
| **调用地址** (`gw.xxx.ti.tencentcs.com/ms-xxx`) | API 调用 (Claude Code 用这个) |
| **WebUI 地址** (`webui.xxx.ti.tencentcs.com`) | 浏览器管理页 (有限制, 见下方) |

#### Claude Code 接入

```bash
export ANTHROPIC_BASE_URL="https://ms-xxx-xxx.gw.ap-zhongwei.ti.tencentcs.com/ms-xxx"
export ANTHROPIC_AUTH_TOKEN="sk-<your-sub2api-api-key>"
claude
```

## 运维

### 查看日志

日志持久化在 CFS 上,从开发机可直接读:

```bash
ssh tione_llmrouter
docker run --rm -v /cfs_turbo/easonsshi/sub2api-data:/cfs:ro alpine \
  tail -100 /cfs/appdata/logs/sub2api.out.log
```

### Admin UI 管理

TI-ONE WebUI 网关 **会剥离 GET 请求的 Authorization header**, 导致登录后所有页面 401。详见 [DECISIONS.md](DECISIONS.md#webui-401-问题)。

解决方案: 在开发机起临时管理容器:

```bash
# 1. 暂停 TI-ONE 服务 (避免两个 PG 同时写同一份 pgdata)
# 2. 在开发机起管理容器
ssh tione_llmrouter
docker run -d --name sub2api-admin --network=host \
  -v /cfs_turbo/easonsshi/sub2api-data:/data/sub2api \
  -e TZ=Asia/Shanghai \
  zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base:sub2api-v3

# 3. 本机 SSH 隧道
ssh -L 8080:127.0.0.1:8080 tione_llmrouter

# 4. 浏览器 http://localhost:8080
# 5. 完成后
docker rm -f sub2api-admin
# 6. 恢复 TI-ONE 服务
```

### 升级

改代码后:

```bash
# 重新 build + push
docker build --network=host -f tione/Dockerfile.tione \
  -t zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base:sub2api-v4 .
docker push zwccr.ccs.tencentyun.com/shennong/eason-ubuntu-base:sub2api-v4

# TI-ONE 控制台更新镜像版本 → 滚动重启
# CFS 数据不动,账号/配置/日志全保留
```

### 修改 youtu 凭证

直接改 CFS 上的文件,重启 TI-ONE 服务即可:

```bash
ssh tione_llmrouter
docker run --rm -v /cfs_turbo/easonsshi/sub2api-data:/cfs alpine \
  sh -c 'echo -e "YOUTU_LLM_BASE=http://112.65.194.90:8001\nYOUTU_LLM_USERNAME=xxx\nYOUTU_LLM_USERID=xxx\nYOUTU_LLM_TOKEN=xxx\nYOUTU_LLM_PROXY_PORT=8088" > /cfs/youtu_proxy/.env'
# 在 TI-ONE 控制台重启服务
```

## 文件清单

| 文件 | 说明 |
|------|------|
| `Dockerfile.tione` | 3-stage 构建: node → golang → ubuntu:24.04 runtime |
| `entrypoint.sh` | 幂等初始化: 目录/PG initdb/config/凭证/环境变量 |
| `supervisord.conf` | 4 进程管理: postgres → redis → youtu_proxy → sub2api |
| `prebake.sh` | 开发机辅助脚本: build / run / publish / clean |
| `youtu_llm_proxy.py` | Anthropic API → 腾讯内部 tRPC 协议转换 sidecar |
| `youtu_proxy.env.example` | youtu 凭证模板 |
| `DECISIONS.md` | 架构决策与踩坑记录 |
