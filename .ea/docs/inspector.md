# Inspector（日志查看器）

> 访问地址：`http://anydev_llmrouter/inspector`（已集成到 Sub2API 前端）

## 概述

Sub2API 内置的请求日志可视化工具。后端为 Go（扩展 `RequestLogReaderService`），前端为 Vue 3 SPA 页面（与 Sub2API 共享认证）。

**2026-05-27 迁移**：从独立 Python FastAPI 服务（端口 8019）迁移为 Sub2API 内置功能。原 systemd 服务 `sub2api-inspector.service` 已停用。

## 架构

```
Browser ──→ Vue SPA /inspector ──→ Go Backend /api/v1/inspector/*
                                        │
                                        ├──→ SQLite request_log.db（WAL 只读）
                                        ├──→ Archive per-day .db files
                                        └──→ PostgreSQL api_keys 表
```

- **统一认证**：使用 Sub2API JWT 认证（所有登录用户可访问，非 admin 专属）
- **SQLite 只读**：以 WAL 模式并发读写不冲突
- **归档支持**：通过 `archive_dir` 配置读取历史日期的独立 .db 文件
- **深度链接**：Usage 页面每条记录可直接跳转到 Inspector 对应条目

## API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/inspector/dates` | 可用日期列表 + 记录数（含归档） |
| `GET` | `/api/v1/inspector/dates/:date/stats` | 当日统计：请求数、按模型/key/session 分组、token 合计 |
| `GET` | `/api/v1/inspector/dates/:date/logs` | 分页日志列表，支持过滤（session_id、api_key_id、model、q） |
| `GET` | `/api/v1/inspector/records/:id` | 按 SQLite PK 查完整记录（含 raw_json） |
| `GET` | `/api/v1/inspector/dates/:date/export` | 过滤结果导出 JSONL |
| `GET` | `/api/v1/inspector/keys` | 所有 API Key 列表（id、name、前缀） |

## 前端功能

- **Stats 卡片**：请求数、input/output/cached token 合计、session 数
- **会话快速筛选**：每个 session 的可点击徽章（蓝色=Codex，粉色=Claude Code）
- **筛选器**：session ID、API Key（下拉）、模型（下拉）、全文搜索
- **请求列表**：时间、Key、模型、session 徽章、in/out/cache token、状态
- **详情面板**：点击任意行 → 右侧面板，含 4 个 Tab：
  - **Chat**：对话可视化（支持 OpenAI 和 Anthropic 格式，含 reasoning、tool_calls、multimodal）
  - **JSON Tree**：可折叠交互式 JSON 树，支持复制
  - **Raw JSON**：格式化 JSON 文本
  - **Headers**：请求头
- **导出**：下载过滤结果为 `.jsonl`
- **分页**：每页 100 条
- **深度链接**：从 Usage 页面点击 "Inspect" 按钮跳转，自动加载对应日期和记录

## 配置

在 `config.yaml` 的 `gateway.request_log` 下：

```yaml
gateway:
  request_log:
    enabled: true
    db_path: /app/data/request_logs/request_log.db
    archive_dir: /apdcephfs/private_easonsshi/data/sub2api_archive/request_logs  # 可选
```

## 文件位置

### Go 后端
- `backend/internal/service/request_log_inspector.go` — Inspector 查询方法
- `backend/internal/handler/inspector_handler.go` — HTTP 处理器（6 个端点）
- `backend/internal/server/routes/user.go` — 路由注册

### Vue 前端
- `frontend/src/views/inspector/InspectorView.vue` — 主页面
- `frontend/src/views/inspector/components/InspectorChatView.vue` — 对话可视化
- `frontend/src/views/inspector/components/InspectorJsonTree.vue` — JSON 树
- `frontend/src/views/inspector/lib/messages.ts` — 消息格式归一化（OpenAI + Anthropic）
- `frontend/src/api/inspector.ts` — API 模块

## 旧版（已归档）

原 Python 实现位于 `.ea/sub2api-inspector/`，仅保留参考，不再部署。
