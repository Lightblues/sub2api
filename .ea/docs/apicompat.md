# API 格式转换

> 核心代码：`backend/internal/pkg/apicompat/`

## 转换拓扑：Hub-and-Spoke

所有格式转换以 **OpenAI Responses API 作为枢纽**（pivot），其他两种格式与它互转：

```
                   ┌──────────────────────┐
                   │  OpenAI Responses API │  ← 枢纽
                   └──────┬───────┬───────┘
                          │       │
             ┌────────────┘       └────────────┐
             ▼                                  ▼
┌──────────────────────┐            ┌──────────────────────────┐
│   Anthropic Messages │            │  OpenAI Chat Completions │
│   /v1/messages       │            │  /v1/chat/completions    │
└──────────────────────┘            └──────────────────────────┘
```

## 转换文件一览

| 文件 | 方向 | 使用场景 |
|------|------|---------|
| `anthropic_to_responses.go` | Anthropic 请求 → Responses 请求 | 用户发 Claude 格式，转发给 OpenAI |
| `responses_to_anthropic.go` | Responses 响应 → Anthropic 响应（含流式） | OpenAI 结果回传给 Claude 客户端 |
| `responses_to_anthropic_request.go` | Responses 请求 → Anthropic 请求 | 用户发 Responses 格式，转发给 Claude |
| `anthropic_to_responses_response.go` | Anthropic 响应 → Responses 响应（含流式） | Claude 结果回传给 Responses 客户端 |
| `chatcompletions_to_responses.go` | ChatCompletions 请求 → Responses 请求 | 用户发旧格式，转发给 OpenAI |
| `responses_to_chatcompletions.go` | Responses 响应 → ChatCompletions 响应（含流式） | OpenAI 结果回传给旧格式客户端 |

## 各方向字段映射

### Anthropic → Responses（请求）

| Anthropic 字段 | Responses 字段 | 备注 |
|----------------|---------------|------|
| `system`（string \| block[]） | `input[0]` role=system | ⚠️ 多 text block 用 `\n\n` 合并，cache_control 丢失 |
| user text | `input_text` | ✅ 无损 |
| user image | `input_image`（data URI） | ✅ base64 → data URI |
| `tool_result` | `function_call_output` | ⚠️ 图片提取到独立 user message |
| `tool_use` | `function_call` | ✅ ID 加 `fc_` 前缀 |
| **`thinking` blocks** | **丢弃** | ❌ OpenAI 不接受 thinking 输入块 |
| `max_tokens` | `max_output_tokens` | ✅ 下限 128 |
| `output_config.effort` | `reasoning.effort` | ⚠️ max→xhigh，其余 1:1 |
| **`stop_sequences`** | **丢弃** | ❌ Responses API 无对应字段 |
| **`thinking.budget_tokens`** | **忽略** | ❌ 只有 effort 生效 |
| `tool_choice` | `tool_choice` | auto→"auto"，any→"required"，none→"none"，tool→function |

固定设置：`Store=false`，`Include=["reasoning.encrypted_content"]`，`Reasoning.Summary="auto"`

### Responses → Anthropic（响应）

| Responses 输出 | Anthropic 块 | 备注 |
|----------------|-------------|------|
| `reasoning` + summary | `thinking` block | ⚠️ 仅 summary 文本，encrypted_content 丢失 |
| `message` output_text | `text` block | ✅ |
| `function_call` | `tool_use` block | ✅ ID 去掉 `fc_` 前缀 |
| `web_search_call` | `server_tool_use` + `web_search_tool_result` | ⚠️ 搜索结果为空数组 |

停止原因映射：`incomplete+max_output_tokens` → `max_tokens`；`completed+最后是tool_use` → `tool_use`；其余 → `end_turn`

> `CacheCreationInputTokens` 恒为 0（Responses API 不提供这个字段）

### Responses → Anthropic（请求，反向）

| 映射 | 备注 |
|------|------|
| `function_call` → 独立 assistant message + `tool_use` block | 每个各生成一条 message |
| `function_call_output` → user message + `tool_result` block | ✅ |
| `reasoning.effort` → `output_config.effort` + `thinking` | ⚠️ budget 使用硬编码默认值（low=1024, medium=4096, high=10240, max=32768） |
| 缺少 `max_output_tokens` | 默认 8192（Anthropic 必填） |
| `mergeConsecutiveMessages()` | Anthropic 要求 user/assistant 交替 |

### ChatCompletions → Responses（请求）

| Chat Completions | Responses | 备注 |
|------------------|-----------|------|
| system message | input role=system | ✅ |
| user text/image_url | `input_text`/`input_image` | ✅，但 `image_url.detail` 丢失 |
| assistant thinking/reasoning | `<thinking>...</thinking>` 标签 | ⚠️ 降级为普通文本标签 |
| `tool_calls[]` | `function_call` items | ✅ |
| `role=tool` | `function_call_output` | ✅ |
| `role=function`（旧式） | `function_call_output`，name 作为 call_id | ⚠️ 语义不一致 |
| `max_tokens`/`max_completion_tokens` | `max_output_tokens` | ✅ 优先 `max_completion_tokens` |
| `stream` | **强制为 true** | ⚠️ 上游始终以流式响应 |
| **`stop`** | **丢弃** | ❌ |

### Responses → ChatCompletions（响应）

| Responses | Chat Completions | 备注 |
|-----------|-----------------|------|
| message output_text | `choices[0].message.content` | ✅ 多段拼接 |
| function_call | `tool_calls[]` | ✅ |
| reasoning summary | `reasoning_content` | ✅ |
| **web_search_call** | **丢弃** | ❌ 静默消费 |

## 信息损失汇总

### 完全丢失的字段

| 方向 | 丢失字段 | 影响 |
|------|---------|------|
| Anthropic→Responses 请求 | `thinking` blocks（历史轮次） | 多轮对话的 thinking 上下文丢失 |
| Anthropic→Responses 请求 | `stop_sequences` | 无法控制停止词 |
| Anthropic→Responses 请求 | `thinking.budget_tokens` | 细粒度 thinking 控制失效 |
| ChatCompletions→Responses 请求 | `stop` | 无法控制停止词 |
| ChatCompletions→Responses 请求 | `image_url.detail` | 图片细节级别丢失 |
| Responses→Anthropic 响应 | `reasoning.encrypted_content` | 不透明 reasoning 数据丢失 |
| Responses→Anthropic 响应 | `web_search_call` 结果 | 转为空结果 |
| Responses→ChatCompletions | `web_search_call` | 完全丢弃 |
| 全部方向 | `CacheCreationInputTokens` | cache 创建 token 数丢失 |

### 语义变换（有损但保留结构）

| 转换 | 变化 |
|------|------|
| Anthropic system blocks[] → string | 多块用 `\n\n` 合并，metadata 丢失 |
| tool_result 图片 | 提取为独立 user message |
| thinking → reasoning summary | 完整 thinking → 仅 summary |
| reasoning_effort `max→xhigh` | 非标准跨平台映射 |
| thinking budget | 原值替换为硬编码默认值 |
| assistant thinking → `<thinking>` 标签 | 结构化 → 纯文本（ChatCompletions 路径） |
| 连续同角色 messages | 合并 content blocks（Anthropic 交替角色要求） |

### 可逆性分析

| 路径 | 可逆？ | 原因 |
|------|--------|------|
| Anthropic→Responses→Anthropic 请求 | ❌ | thinking blocks 丢失、system 扁平化、stop_sequences 丢失 |
| Responses→Anthropic→Responses 请求 | ⚠️ 近似可逆 | thinking budget 变为硬编码、tool ID 转换 |
| ChatCompletions→Responses→ChatCompletions 请求 | ❌ | stream 强制 true、stop 丢失、image detail 丢失 |

## 流式转换状态机

### Responses → Anthropic（`ResponsesEventToAnthropicState`）

```
response.created           → message_start
output_item.added(func)    → content_block_start(tool_use)
output_item.added(reason)  → content_block_start(thinking)
output_text.delta          → [自动打开 text block] + content_block_delta(text_delta)
func_args.delta            → content_block_delta(input_json_delta)
reasoning_summary.delta    → content_block_delta(thinking_delta)
*.done                     → content_block_stop
response.completed         → message_delta + message_stop
```

状态字段：`ContentBlockIndex`（自增）、`ContentBlockOpen`、`CurrentBlockType`、`OutputIndexToBlockIdx` map

### Anthropic → Responses（`AnthropicEventToResponsesState`）

```
message_start              → response.created
content_block_start(think) → output_item.added(reasoning)
content_block_start(text)  → output_item.added(message)
content_block_start(tool)  → output_item.added(function_call)
content_block_delta(text)  → output_text.delta
content_block_delta(think) → reasoning_summary_text.delta
content_block_delta(json)  → function_call_arguments.delta
content_block_stop         → output_item.done / output_text.done / 等
message_delta              → 捕获 usage
message_stop               → response.completed
```

### Responses → ChatCompletions（`ResponsesEventToChatState`）

```
response.created           → role chunk（role="assistant"）
output_text.delta          → delta.content chunk
output_item.added(func)    → delta.tool_calls[{index, id, name}]
func_args.delta            → delta.tool_calls[{index, arguments}]
reasoning_summary.delta    → delta.reasoning_content
response.completed         → finish_reason chunk + 可选 usage chunk
```
