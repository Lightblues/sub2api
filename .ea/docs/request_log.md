# 请求日志（SQLite）

> 对应英文 spec：`spec/llm_sub2api/request_log.md`

## 概述

在 sub2api Go 后端的 OpenAI 网关中，把每条完整的请求/响应异步写入 SQLite 数据库，用于训练数据采集和调试分析。

## 工作原理

1. SSE 流式响应中，`response.completed` 事件包含完整响应（reasoning、output、usage）
2. 流扫描循环捕获该事件，存入结果结构体
3. 流结束后，goroutine 异步提取索引字段（model、session、usage 等），写入 SQLite，`raw_json` 存完整 JSON

## 涉及代码文件

| 文件 | 改动内容 |
|------|---------|
| `backend/internal/config/config.go` | 新增 `GatewayRequestLogConfig` 结构体（含 `DbPath`） |
| `backend/internal/service/openai_gateway_service.go` | 在两条流式路径中捕获 `completedEventData` |
| `backend/internal/service/request_log.go` | SQLite 写入器，提取索引字段 |
| `backend/internal/service/request_log_reader.go` | SQL 查询接口（供 admin API 调用） |

## 两条流式路径

| 路径 | 函数 | 触发条件 |
|------|------|---------|
| Passthrough | `forwardOpenAIPassthrough` → `handleStreamingResponsePassthrough` | OAuth 账号开启 passthrough 时 |
| Non-passthrough | `Forward` → `handleStreamingResponse` | 其他所有账号（大多数 Codex 流量） |

> ⚠️ 初版只覆盖 passthrough 路径，导致 Codex 请求（non-passthrough）不写日志，现已修复。

## 配置（config.yaml）

```yaml
gateway:
  request_log:
    enabled: true
    dir: "data/request_logs"
    db_path: "data/request_logs/request_log.db"   # 默认 {dir}/request_log.db
    platforms:
      - openai
      - anthropic
    excluded_groups:
      - private
```

容器内路径：`/app/data/request_logs/request_log.db`
宿主机实际路径：`/data/docker/lib/volumes/sub2api_sub2api_data/_data/request_logs/request_log.db`

## SQLite Schema

```sql
CREATE TABLE records (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    date             TEXT NOT NULL,          -- '2026-03-31'（分区键）
    ts               INTEGER NOT NULL,       -- Unix 时间戳
    api_key_id       INTEGER,
    model            TEXT,                   -- 从请求体或响应中提取
    session_type     TEXT,                   -- 'codex' | 'claude-code'
    session_id       TEXT,
    status           TEXT,
    input_tokens     INTEGER DEFAULT 0,
    output_tokens    INTEGER DEFAULT 0,
    cached_tokens    INTEGER DEFAULT 0,
    reasoning_tokens INTEGER DEFAULT 0,
    total_tokens     INTEGER DEFAULT 0,
    raw_json         TEXT NOT NULL           -- 完整 JSON
);
```

## 记录格式（实际）

`request_body` 是 Anthropic Messages 格式（CC 客户端发出），`response_complete` 是 OpenAI Responses 格式：

```json
{
  "ts": 1773971509,
  "api_key_id": 3,
  "request_body": {
    "model": "claude-sonnet-4-6",
    "system": [{"type": "text", "text": "You are Claude Code..."}],
    "messages": [...],
    "tools": [...],
    "max_tokens": 16000,
    "stream": true,
    "thinking": {"type": "enabled", "budget_tokens": 10000}
  },
  "response_complete": {
    "type": "response.completed",
    "response": {
      "id": "resp_...",
      "model": "gpt-5.4",
      "output": [
        {"type": "reasoning", "summary": [...]},
        {"type": "function_call", "name": "Bash", "arguments": "..."},
        {"type": "message", "content": [...]}
      ],
      "usage": {"input_tokens": 23106, "output_tokens": 530, "total_tokens": 23636}
    }
  }
}
```

## 覆盖率验证（2026-03-23）

| API 路径 | 格式 | 是否记录 |
|---------|------|---------|
| `POST /v1/messages`（text） | Anthropic Messages | ✅ |
| `POST /v1/messages`（tool_use） | Anthropic Messages | ✅ |
| `POST /v1/chat/completions` | OpenAI ChatCompletions | ❌ **未记录** |
| `POST /v1/responses`（text） | OpenAI Responses | ✅ |
| `POST /v1/responses`（tool_use） | OpenAI Responses | ✅ |

> 🐛 **已知 Bug**：`/v1/chat/completions` 路径（`openai_gateway_chat_completions.go`）没有调用日志写入，TODO 中。Claude Code 走 `/v1/messages`，不受影响。

## 内容完整性测试（2026-03-21，76 条记录）

| 检查项 | 结果 |
|--------|------|
| model 字段存在 | 76/76 ✅ |
| messages（完整对话历史） | 76/76 ✅ |
| system prompt | 74/76 ✅（2 条为手动测试） |
| tools 定义（~35个/条） | 74/76 ✅ |
| response 存在 | 76/76 ✅ |
| usage 统计 | 76/76 ✅ |
| function_call arguments 合法 JSON | 169/169 ✅ |

## 已修复问题

### output 为空（2026-04-10 修复）

**现象**：2026-04-07 之后的日志中 `response_complete.response.output` 为空数组。

**原因**：OpenAI 上游 API 行为变更——`response.completed` 终态 SSE 事件不再包含 `output` 内容，改为通过流式 delta 事件（`response.output_text.delta`、`response.function_call_arguments.delta`、`response.reasoning_summary_text.delta`）增量下发。上游 sub2api 已在非流式路径修复（`BufferedResponseAccumulator`），但流式路径的 request_log 捕获仍取原始终态事件。

**修复**：在两条流式路径（`handleStreamingResponsePassthrough`、`handleStreamingResponse`）中添加 `BufferedResponseAccumulator`，累积 delta 事件内容。流结束后通过 `patchCompletedEventDataOutput` 检测 output 是否为空，为空时用累积内容重建并 patch 到 `completedEventData` 中再写入日志。

## 已知局限

1. **格式不对称**：request_body 是 Anthropic 格式，response_complete 是 OpenAI Responses 格式，做训练数据时需要转换
2. **reasoning summary 空值**：约 18/59 条推理响应的 summary 为空（上游 API 行为，非日志 bug）
3. **`/v1/chat/completions` 无日志**：见上方 Bug
4. **2026-04-07 ~ 修复部署前的日志不可恢复**：该时段的日志 output 为空，因为原始 delta 内容未被记录
