# eason 分支旧源码参考

这里存放的是从原 `eason` 分支 (基线 v0.1.125) 拷贝过来的自定义源码文件,**仅供参考**,不参与编译。

| 目录 | 内容 |
|------|------|
| `backend-legacy/` | Go 后端 — `request_log*.go`, `inspector_handler.go` (需要重新适配 v0.1.150 的新架构) |
| `frontend-legacy/` | Vue 前端 — Inspector view + UseKeyModal + AppHeader (需要重新适配 v0.1.150 的 i18n 结构) |
| `deploy-legacy/` | Dockerfile / docker-compose / entrypoint (含 youtu_llm_proxy 相关逻辑,现已放弃) |

## 关键 API 变化 (v0.1.125 → v0.1.150)

### 后端

- `gateway_service.go` (9557 行) 拆分成:
  - `gateway_forward.go` — `Forward()`
  - `gateway_upstream_response.go` — `handleStreamingResponse()`, `handleNonStreamingResponse()`, `type streamingResult`
  - `gateway_upstream_request.go` — `buildUpstreamRequest()`
  - `gateway_scheduling.go`, `gateway_count_tokens.go`, `gateway_anthropic_passthrough.go`, etc.
- OpenAI 侧类似:
  - `openai_gateway_service.go` → `openai_gateway_forward.go` + `openai_gateway_response_handling.go` + 一堆
- **`AccountTypeUpstream` 已成为上游一等公民** — 但语义可能与 eason 版本不同,重启用前需确认

### 前端

- `frontend/src/i18n/locales/en.ts` / `zh.ts` **被删除** — 上游改为按模块拆分翻译文件
- `frontend/src/views/HomeView.vue` 上游有新版本,eason 分支曾删除过

## 复用建议

- Request Log 后端: `.ea/reference/backend-legacy/request_log*.go` 里的 SQLite schema / SSE 累积器 / reader API 可以直接抄,但集成点 (Forward / handleStreaming 调用位置) 要重新映射
- Inspector 前端: `.ea/reference/frontend-legacy/inspector-view/` 的 Vue 组件基本可以整个 copy 过去,只需要调 API 路径 & i18n 引用
- UseKeyModal: `.ea/reference/frontend-legacy/UseKeyModal.eason.vue` 里的 Claude Code 配置模板可以 diff 出 400k context / effort / attribution 相关的改动补丁
