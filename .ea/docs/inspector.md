# Inspector（日志查看器）

> 部署地址：`http://anydev_llmrouter:8019`

## 概述

Web 工具，用于可视化和调试 sub2api 请求日志。Python FastAPI 后端（只读） + 单文件 HTML 前端（Tailwind CDN + 原生 JS）。

## 架构

```
Browser ──→ FastAPI (:8019) ──→ SQLite（只读）
               │                    ↑ Go 后端写入
               │              request_log.db
               └──→ PostgreSQL（docker exec psql）──→ api_keys 表
```

- **Inspector 只读**：以 WAL 模式打开 SQLite，和 Go 后端并发读写不冲突
- **API Key 名称**：通过 `docker exec` 查询 sub2api-postgres 容器的 `api_keys` 表
- **DB 凭据**：自动从 sub2api `config.yaml` 读取（先尝试 `/root/sub2api/config.yaml`，再尝试 docker volume 路径）
- **Stats 缓存**：首次请求时计算，行数变化时失效

## 包结构

```
packages/sub2api-inspector/
├── pyproject.toml                    # hatchling + uvicorn + fastapi + orjson + pyyaml
└── src/sub2api_inspector/
    ├── __init__.py
    ├── db.py                         # SQLite 只读查询
    ├── server.py                     # FastAPI + 全部接口
    └── static/index.html             # 单文件前端
```

入口命令：`sub2api-inspector`（uv workspace 管理）

## API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/` | 前端 HTML |
| `GET` | `/api/dates` | 可用日期列表 + 文件大小 |
| `GET` | `/api/logs/{date}/stats` | 当日统计：请求数、按模型/key/session 分组、token 合计 |
| `GET` | `/api/logs/{date}` | 分页日志列表，支持过滤（session_id、api_key_id、model、q） |
| `GET` | `/api/logs/{date}/record/{id}` | 按 ID 查完整记录 |
| `GET` | `/api/logs/{date}/export` | 过滤结果导出 JSONL |
| `GET` | `/api/keys` | 所有 API Key 列表（id、name、前缀） |
| `GET` | `/api/keys/lookup?sk=...` | 按 sk- 前缀查 key ID |
| `POST` | `/api/admin/resync` | 强制从 JSONL 重新同步（可选 `?date=`） |

## 会话 ID 提取逻辑

两种客户端存放 session ID 的位置不同：

| 客户端 | 位置 | 示例 |
|--------|------|------|
| Codex（VS Code / Desktop） | `request_headers.session_id` | `019d3ec1-e08f-7561-...` |
| Claude Code CLI | `request_headers.x-claude-code-session-id` | `6c5a4a8b-c72c-44df-...` |
| Claude Code CLI（降级） | `request_body.metadata.user_id`（JSON string）→ `.session_id` | 同上 |

`_extract_session()` 按顺序尝试三个位置。

## 前端功能

- **Stats 面板**：请求数、input/output/cached token 合计、session 数、模型分布、Key 分布
- **会话快速筛选**：每个 session 的可点击徽章（蓝色=Codex，粉色=Claude Code）
- **筛选器**：session ID、API Key（带名称下拉）、模型、全文搜索
- **Key 查找**：输入 `sk-...` 前缀 → 显示 key ID 和名称，一键过滤
- **请求列表**：时间、Key（带名称）、模型、session 徽章、in/out/cache token、状态
- **详情面板**：点击任意行 → 右侧面板，含 Full JSON / Headers / Request Body / Response 四个 Tab，支持语法高亮和复制
- **导出**：下载过滤结果为 `.jsonl`
- **分页**：每页 100 条

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `INSPECTOR_DB` | 自动探测 `request_log.db` | SQLite 路径 |
| `CONFIG_PATH` | 自动探测 | sub2api `config.yaml` 路径（用于读 PG 凭据） |

## TODO

- [ ] systemd 服务（持久化部署，现在是 nohup）
- [ ] Token 费用估算（接入模型定价）
- [ ] 请求量 / token 用量时序图
- [ ] 大日期 stats 流式返回（避免 multi-GB 文件超时）
- [ ] 鉴权（basic auth 或 token）
