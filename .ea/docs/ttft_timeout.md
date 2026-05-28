# 首 Token 超时（TTFT Timeout）实现报告

> 日期：2026-05-27  
> 状态：已上线（`sub2api:eason`，`anydev_llmrouter`）  
> 影响范围：仅分组 `ian_private`（group id=24，OpenAI OAuth Codex）

## 背景

`ian_private` 分组上的 GPT/Codex 请求，绝大多数首 token 延迟（TTFT）在 5s 以内，但存在长尾：部分请求首 token 需 **100s+** 才返回，而首 token 之后的输出速度正常。推测原因为上游排队或连接池等待，而非模型生成本身变慢。

**目标**：若首 token 超过 **15s** 仍未到达，主动将请求标记为失败，使下游应用（如 Claude Code）触发重试，避免用户长时间挂起。

**约束**：策略仅对 `ian_private` 生效，不影响其他 API 分组。

## 调研结论（摘要）

| 方案 | 可行性 | 说明 |
|------|--------|------|
| HTTP 请求整体 `timeout=15s` | ❌ | 会中断首 token 之后的长生成 |
| `response_header_timeout=15s` | ❌ | 流式响应头通常很快返回 200，无法约束 body 内首 token |
| 调低 `stream_data_interval_timeout` 至 15s | ❌ | 度量的是**任意两次上游读**之间的间隔；preamble 事件会重置计时器，且会误杀生成中的正常停顿 |
| 透传客户端 `x-stainless-timeout` | ⚠️ | 默认被网关剥离；且多为整请求超时，非 TTFT 专用 |
| **独立 TTFT watchdog** | ✅ | 从请求开始计时，直到第一个**非 preamble** SSE 事件 |

### 数据佐证（`ian_private`，近 7 天 usage_logs）

| 指标 | 数值 |
|------|------|
| 有效样本 | 8,417 |
| P50 TTFT | ~2.1s |
| P95 TTFT | ~7.4s |
| P99 TTFT | ~340s |
| TTFT > 15s | 251（~3.0%） |
| TTFT > 60s | 201（~2.4%） |

该分组当前仅 1 个 OAuth 账号（`NoahBarnes@gmailso.com`），请求类型均为 HTTP 流式 `/v1/responses`（`RequestTypeStream`）。

## 设计

### 计时规则

- **起点**：网关开始处理上游流式响应（`startTime`）。
- **终点**：收到第一个「对客户可见输出」的 SSE 事件，与现有 `first_token_ms` 统计口径一致。
- **不计入 TTFT 的事件**（preamble）：
  - `response.created`
  - `response.in_progress`

### 超时后行为

1. 关闭上游 `resp.Body`，停止阻塞读取。
2. 若客户端仍连接，写入 OpenAI Responses 兼容的 SSE 错误事件：
   - `type`: `error`
   - `error.code` / `error.message`: `first_token_timeout`
3. 返回 Go error：`first token timeout`。
4. Handler 层：首 token 前未写 body → **502 JSON**；已写 SSE → 流内 error 事件后结束。

### 与现有超时的关系

| 配置项 | 默认值 | 作用 |
|--------|--------|------|
| `stream_data_interval_timeout` | 180s | 两次上游读之间无数据（含 preamble） |
| `group_first_token_timeout_seconds` | 按分组 | **仅**限制「首 token 前」等待时间 |

二者独立：TTFT 超时解决排队长尾；流间隔超时解决传输中途卡死。

## 实现

### 配置

```yaml
# config.yaml（挂载为容器内 /app/data/config.yaml）
gateway:
  group_first_token_timeout_seconds:
    ian_private: 15
```

- Key 为 **分组名**（`groups.name`），值为秒数；`0` 或未配置表示该分组不启用。
- 示例模板：`deploy/config.example.yaml`

### 代码位置

| 文件 | 说明 |
|------|------|
| `backend/internal/config/config.go` | `GatewayConfig.GroupFirstTokenTimeoutSeconds` |
| `backend/internal/service/openai_stream_ttft_timeout.go` | 配置解析、`openAIGroupFirstTokenTimeout()` |
| `backend/internal/service/openai_gateway_service.go` | `handleStreamingResponse`：select 循环增加 `ttftCh` |
| 同上 | `handleStreamingResponsePassthrough`：TTFT>0 时走 goroutine+select |
| `backend/internal/service/openai_stream_ttft_timeout_test.go` | 单元测试 |

### 路径覆盖

- **Non-passthrough**（`handleStreamingResponse`）：`ian_private` 当前 OAuth 账号 `openai_passthrough=false`，走此路径。
- **Passthrough**（`handleStreamingResponsePassthrough`）：若日后开启透传，同样受 TTFT 限制。

未改 WebSocket v2 路径：`ian_private` 流量为 HTTP SSE，无 WS。

## 运维

### 生效方式

修改 `config.yaml` 后重建并重启：

```bash
cd /root/sub2api
# 编辑 config.yaml 中 group_first_token_timeout_seconds
docker build -t sub2api:eason -f Dockerfile .
docker compose up -d sub2api
```

无需数据库迁移。

### 调参建议

| 阈值 | 预期效果 |
|------|----------|
| 15s（当前） | 约拦截 3% 请求（TTFT>15s），P95 约 7.4s，误杀风险较低 |
| 10s | 更激进，可能误杀慢启动的正常请求 |
| 20s | 更保守，长尾排队仍可能等较久 |

增加其他分组：在 map 下追加 `分组名: 秒数` 即可。

### 日志

超时时会打印（legacy log）：

```
First token timeout: account=<id> model=<model> timeout=15s
```

Passthrough 路径前缀为 `[OpenAI passthrough] First token timeout: ...`。

### 验证

```bash
# 单元测试（需 Go 1.26+）
cd backend && go test ./internal/service/ -run 'TestGroupFirstTokenTimeout|TestOpenAIStreamingFirstTokenTimeout' -count=1
```

线上观察：Usage 页 `first_token_ms` 分布、Claude Code 侧是否出现快速失败+重试而非 100s+ 挂起。

## 限制与后续

1. **无账号 failover**：`ian_private` 仅 1 账号，超时后依赖**客户端重试**新请求，不会在网关内切换账号。
2. **不计费**：`Forward` 失败且 `result==nil` 时不写 usage，首 token 前超时通常无 token 计费。
3. **preamble 心跳**：若上游持续发送 `response.in_progress` 但不发实质 output，仍会在 15s 触发超时（符合「排队过久即失败」意图）。
4. **可选后续**：管理后台可配置分组 TTFT；Ops 指标对 `first_token_timeout` 单独计数。

## 相关文档

- [架构.md](./architecture.md) — 网关与流式处理概览  
- [运维日志.md](./ops_log.md) — 部署与变更记录  
- [请求日志.md](./request_log.md) — `first_token_ms` 字段来源（PostgreSQL usage_logs）
