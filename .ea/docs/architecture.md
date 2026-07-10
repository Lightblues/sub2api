# 架构

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.25 + Gin + Ent ORM + Google Wire DI |
| 前端 | Vue 3 + Vite + TailwindCSS（Admin Dashboard） |
| 存储 | PostgreSQL 15 + Redis 7 |
| 部署 | Docker Compose |

## 目录结构

```
backend/
  cmd/server/          # 入口 + Wire 依赖注入配置
  ent/                 # Ent ORM schema 定义
  internal/
    config/            # Viper 配置加载
    domain/            # 平台/账号类型常量
    handler/           # HTTP handler（gateway、admin、user）
    middleware/        # 认证、CORS、限流
    model/             # DTO 数据传输对象
    pkg/apicompat/     # ★ 核心：多平台 API 格式互转
    pkg/antigravity/   # Google Cloud Code（Gemini）适配器
    repository/        # 数据访问（Ent + Redis 两级缓存）
    server/            # 路由注册
    service/           # 业务逻辑（100+ services）
    setup/             # 初始化向导
frontend/              # Vue 3 管理后台 SPA
deploy/                # docker-compose、配置示例、.env
```

## 支持的平台

| 平台标识 | 上游地址 | 说明 |
|---------|---------|------|
| `anthropic` | api.anthropic.com | Claude API |
| `openai` | api.openai.com | OpenAI API |
| `gemini` | generativelanguage.googleapis.com | Google Gemini |
| `antigravity` | cloudcode-pa.googleapis.com | Google Cloud Code（Gemini v1internal） |
| `sora` | OpenAI | Sora 图片生成 |

## 账号类型

| 类型 | 说明 |
|------|------|
| `oauth` | 完整 OAuth 账号（profile + inference） |
| `setup-token` | 仅推理的 setup token |
| `apikey` | 标准 API Key |
| `upstream` | 透传代理（Base URL + API Key） |

## 账号选择策略（4层）

在 `gateway_service.go` 中实现，依次尝试：

1. **模型路由** — 配置中指定特定模型走特定账号
2. **粘性会话** — `hash(system_prompt + first_message)` → session ID → 缓存账号映射（同一对话总打到同一账号）
3. **负载感知选择** — 按优先级、当前负载、LRU `last_used_at` 排序
4. **等待队列** — 所有账号忙时排队，`ping_interval` 轮询

## 计费模式

| 模式 | 机制 |
|------|------|
| `standard` | 按 token 消费余额（pay-per-token） |
| `subscription` | 月配额 + 日/周/月上限 |

热路径使用两级缓存：L1 ristretto 内存缓存 + L2 Redis。

## API 路由

### 网关路由（API Key 鉴权）

| 路由 | 说明 |
|------|------|
| `POST /v1/messages` | Claude Messages API（按 group platform 分发） |
| `POST /v1/messages/count_tokens` | Claude token 计数 |
| `GET /v1/models` | 模型列表 |
| `POST /v1/responses` | OpenAI Responses API |
| `POST /v1/chat/completions` | OpenAI ChatCompletions API |
| `GET /v1beta/models` | Gemini 模型列表 |
| `POST /v1beta/models/*modelAction` | Gemini generateContent |
| `POST /antigravity/v1/messages` | 强制走 Antigravity 平台 |
| `POST /sora/v1/chat/completions` | Sora 图片生成 |

### 管理路由（JWT 鉴权）

| 路由 | 说明 |
|------|------|
| `/api/v1/auth/*` | 登录、注册、登出、OAuth、密码重置 |
| `/api/v1/user/*` | 个人信息、API Key 管理、用量统计 |
| `/api/v1/admin/*` | 全量管理（用户、账号、分组、代理、设置、运维） |

### 请求中间件链

```
RequestBodyLimit → ClientRequestID → OpsErrorLogger → APIKeyAuth → RequireGroupAssignment → Platform Handler
```

## 配置项

### config.yaml（文件配置，Viper）

| 配置节 | 关键字段 |
|--------|---------|
| `server` | host、port、mode（debug/release）、h2c、trusted_proxies |
| `run_mode` | `standard`（完整计费）或 `simple`（跳过计费） |
| `database` | PostgreSQL 连接池 |
| `redis` | 连接池、超时 |
| `gateway` | max_body_size、connection_pool_isolation、stream_keepalive_interval、max_line_size、failover_on_400 |
| `jwt` | secret、access/refresh token 有效期 |
| `token_refresh` | enabled、check_interval、refresh_before_expiry、max_retries |
| `billing` | 熔断器配置 |
| `gemini` | OAuth client_id/secret、quota tiers |
| `ops` | 监控开关、清理计划 |
| `pricing` | 远程定价 URL、更新间隔 |
| `concurrency` | 等待队列的 ping_interval |

### Admin Settings（数据库存储，通过 `/api/v1/admin/settings` 管理）

| 分类 | 配置项 |
|------|--------|
| 注册 | 邮箱验证、后缀白名单、邀请码 |
| 认证 | SMTP、Cloudflare Turnstile CAPTCHA、TOTP 二步验证、LinuxDo OAuth |
| 品牌 | site_name、site_logo、site_subtitle（OEM） |
| 默认值 | 用户并发数、初始余额、订阅套餐 |
| 平台 | Gemini 配额策略、各平台模型降级 |
| 高级 | Antigravity identity patch（Claude → Gemini systemInstruction 注入） |
| 运维 | 监控、告警规则、定期报告 |
