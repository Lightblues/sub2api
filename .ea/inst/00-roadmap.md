# eason-v2 路线图

> 基线: `upstream/v0.1.150` (commit `0dec1ad2`)
> 目标: 在干净的 v0.1.150 上重新实施 eason 分支必要的自定义功能,放弃已过时的补丁。

## 保留功能清单

| # | 功能 | 优先级 | 状态 |
|---|------|-------|------|
| 1 | Request Log (SQLite) | P0 | 待实施 |
| 2 | Inspector 可视化前端 | P0 | 待实施 |
| 3 | LLM Router 品牌重命名 | P1 | 待实施 |
| 4 | Claude Code 配置模板改进 (UseKeyModal + 400k context + effort/attribution env) | P2 | 待实施 |

## 放弃功能

| # | 功能 | 原因 |
|---|------|------|
| - | TTFT timeout (`openai_stream_ttft_timeout.go`) | 只对 `ian_private` 一个分组;v0.1.150 已改进 SSE keepalive / stream bridge |
| - | Youtu LLM Proxy sidecar + `AccountTypeUpstream` 全套支持 | 不再使用腾讯内部 tRPC Claude |
| - | tione 部署 | 已废弃 |
| - | Sora OAuth `client_id` 切换 | 不再需要 |
| - | pnpm@9 pin | v0.1.150 已内建 |

## 参考资料位置

- `.ea/docs/` — 原 eason 分支的功能文档 (架构/请求日志/Inspector 等)
- `.ea/reference/backend-legacy/` — 原 eason 的后端源码 (request_log*.go, inspector_handler.go)
- `.ea/reference/frontend-legacy/` — 原 eason 的前端 Vue 组件 (inspector view, UseKeyModal, AppHeader)
- `.ea/reference/deploy-legacy/` — 原 eason 的 Dockerfile / docker-compose / entrypoint

## 分支管理

- `eason-v2` — 新主分支,基于 v0.1.150
- `eason` — 原分支,保留作为 legacy 参考 (不再更新)
- 待 eason-v2 稳定并部署验证后,可将 `eason` 备份为 `eason-legacy`,然后把 `eason-v2` 重命名为 `eason`

## 实施顺序

1. ✅ 骨架 commit (本文档 + `.ea/reference/`)
2. 验证 v0.1.150 基线 `go build` 通过 (本地无 go/docker 工具链,需在 anydev_llmrouter 上验证)
3. Feature 1: Request Log
4. Feature 2: Inspector 前端
5. Feature 3: LLM Router 品牌
6. Feature 4: Claude Code 配置模板
7. 本地 docker build 验证
8. Push + devcloud 部署

## 关键设计决策 (待确认)

### Request Log

- **v0.1.150 上游是否已有 raw JSON 记录机制?** — 需要先探查 upstream 的 `usage_logs` / ops 相关表是否已能覆盖训练数据采集需求
- **技术方向**: 沿用原 eason 的独立 SQLite 方案,还是复用 upstream 的 postgres 存储?
- **代码集成点**: v0.1.150 把 `gateway_service.go` 拆成了 `gateway_forward.go` / `gateway_upstream_response.go` 等,SSE 事件累积器需要重新映射到 `handleStreamingResponse` (在 `gateway_upstream_response.go`) 及 OpenAI 侧的 `openai_gateway_response_handling.go`

### Inspector

- API 前缀保持 `/api/v1/inspector/*` 兼容旧客户端 (虽然只有 Vue 前端调用)
- 路由挂载点: `frontend/src/router/index.ts` 里加 `/inspector` 路由
- 上游 v0.1.150 已迁移到 auto-generated i18n locales (`locales/en.ts` 被删,分散到各模块),需要相应重新组织翻译文案
