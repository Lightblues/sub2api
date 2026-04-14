# ChatCompletions 代理调研

> 日期：2026-03-26

## 问题背景

很多 API 提供商（第三方、本地模型等）只暴露 OpenAI ChatCompletions 格式，而 Claude Code 只用 Anthropic Messages API。需要一个翻译代理做中间层。

## 开源方案对比

### 轻量代理

| 项目 | Stars | 语言 | 架构 | 主要问题 |
|------|-------|------|------|---------|
| [maxnowack/anthropic-proxy](https://github.com/maxnowack/anthropic-proxy) | ~409 | JS/Fastify | 单文件 ~350 行 | tool_call 块索引 bug；流式 error 崩溃；thinking 块类型错误 |
| [m0n0x41d/anthropic-proxy-rs](https://github.com/m0n0x41d/anthropic-proxy-rs) | ~29 | Rust/Axum | ~600 行，7 个文件 | 代码质量最好，thinking 正确，用户少 |
| [1rgs/claude-code-proxy](https://github.com/1rgs/claude-code-proxy) | — | Python/FastAPI+LiteLLM | 单文件 1,522 行 | 🔴 tool_result 转成纯文本（破坏 agentic 循环） |
| [fuergaosi233/claude-code-proxy](https://github.com/fuergaosi233/claude-code-proxy) | — | Python/FastAPI+LiteLLM | fork 自 1rgs | 同上 |

### 1rgs/claude-code-proxy 详细分析

**能用的**：基本文本流、模型映射、流式 tool_calls → `input_json_delta`、Gemini schema 清理、多 provider 支持

**致命问题**：

| 问题 | 严重度 | 说明 |
|------|--------|------|
| **tool_result 转成纯文本** | 🔴 致命 | 转成 `"Tool result for {tool_id}:\n{content}"`，LLM 看到的是散文而非结构化结果，多轮工具调用循环彻底崩溃 |
| **非流式 tool_calls 降级** | 🔴 严重 | `is_claude_model` 检查导致非 Claude 模型的 tool_calls 变成文本 |
| **thinking 静默丢弃** | 🟡 | 非 Anthropic provider 完全忽略 thinking 配置 |
| **max_tokens 硬编码 16K** | 🟡 | CC 经常请求 128K+ |
| **图片 → placeholder 文本** | 🟡 | `"[Image content - not displayed]"` |

代码质量：3/10，单文件、大量重复、裸 `except:`。

### LiteLLM（重但完整）

核心代码：`litellm/llms/anthropic/experimental_pass_through/adapters/`（约 3,900 行）

**做对了的**：

| 功能 | 实现 |
|------|------|
| tool_result | ✅ 正确转成 `role: "tool"` + `tool_call_id` |
| 工具名截断 | ✅ OpenAI 64 字符限制 → `{55前缀}_{8位sha256}`，响应时还原 |
| thinking → reasoning | ✅ `budget_tokens` → `reasoning_effort`（≥10K→high，≥5K→medium，≥2K→low） |
| 响应中的 thinking | ✅ `reasoning_content` → Anthropic `thinking` block |
| 流式状态机 | ✅ 完整 content_block 生命周期 |
| 图片/文档 | ✅ base64 转换 |

**顾虑**：
- 目录名就叫 `experimental_pass_through`
- 依赖巨重（100+ 包，~500MB Docker 镜像）
- 主路径是反向（OpenAI 进，任意出），Anthropic 进是次要路径
- `budget_tokens` → `reasoning_effort` 是有损启发式

### 三方案对比

| | 1rgs proxy | LiteLLM | 自研 Go |
|--|-----------|---------|--------|
| 代码量 | 1,522 行 | ~3,900 行 | ~500-800 行 |
| Docker 大小 | ~200MB | ~500MB+ | ~20MB |
| tool_result | 🔴 纯文本 | ✅ | 待实现 |
| tool_calls 流式 | ✅ | ✅ | 待实现 |
| thinking 支持 | 🔴 丢弃 | ✅ 有损映射 | 待实现 |
| 图片 | 🔴 placeholder | ✅ base64 | 待实现 |
| 工具名截断 | ❌ | ✅ | 可选 |
| CC 工具循环稳定 | ❌ | ✅ | 视实现 |

## 架构设计

### 方案 A：经由 Responses 枢纽（sub2api 风格）

```
Anthropic 请求 → AnthropicToResponses()  [现有]
              → ResponsesToChatCompletions() [新]
              → POST /v1/chat/completions
              → ChatChunkToResponsesEvents() [新]
              → ResponsesEventToAnthropic()  [现有]
              → Anthropic SSE
```

- ✅ 复用 ~70% sub2api 代码
- ❌ 两跳转换，thinking 经 Responses 层后只剩 summary
- ❌ 更难调试

### 方案 B：直接转换（推荐）

```
Anthropic 请求 → AnthropicToChatCompletions() [新]
              → POST /v1/chat/completions
              → ChatStreamToAnthropicEvents() [新]
              → Anthropic SSE
```

- ✅ 一跳，损失最少
- ✅ 只有两个函数，易于调试
- ✅ 约 500-800 行
- **推荐独立实现时用此方案**

### 字段映射（方案 B）

**请求：Anthropic → ChatCompletions**

```
Anthropic                          Chat Completions
─────────────────────────────────────────────────────
system                           → messages[0] role="system"
user text                        → messages[] role="user"
user image                       → messages[] content[type=image_url]
assistant text                   → messages[] role="assistant"
assistant thinking               → 丢弃或厂商特定处理
tool_use block                   → tool_calls[{id, function{name, arguments}}]
tool_result block                → messages[role=tool, tool_call_id, content]  ← 关键！
max_tokens                       → max_tokens / max_completion_tokens
stop_sequences                   → stop  ✅（直接转换保留此字段！）
thinking budget_tokens           → reasoning_effort（启发式）
tools[].input_schema             → tools[].function.parameters
tool_choice                      → tool_choice（auto/required/none/function）
```

**流式响应：ChatCompletions → Anthropic SSE**

```
Chat Completions Chunk              Anthropic SSE 事件
──────────────────────────────────────────────────────────
（首个 chunk，role=assistant）    → message_start
delta.content                     → content_block_start(text) + content_block_delta
delta.reasoning_content           → content_block_start(thinking) + content_block_delta
delta.tool_calls[i]（首次，含 name）→ content_block_start(tool_use, id, name)
delta.tool_calls[i]（后续，参数）  → content_block_delta(input_json_delta)
finish_reason="stop"              → message_delta(end_turn) + message_stop
finish_reason="tool_calls"        → message_delta(tool_use) + message_stop
finish_reason="length"            → message_delta(max_tokens) + message_stop
usage chunk                       → 填入 message_start 和 message_delta 的 usage
```

## 关键陷阱

### 1. Tool Result 处理（最关键）

Claude Code 的核心工作流是多轮工具调用：

```
CC 发请求 → 模型返回 tool_use → CC 执行工具 → CC 发 tool_result → 模型继续...
```

**必须转成 OpenAI `role: "tool"` 消息，绝对不能转成纯文本！**

```json
// Anthropic tool_result：
{"role": "user", "content": [{"type": "tool_result", "tool_use_id": "toolu_xxx", "content": "..."}]}

// 必须变成：
{"role": "tool", "tool_call_id": "call_xxx", "content": "..."}

// 转成这样就会彻底崩溃：
"Tool result for toolu_xxx:\n..."
```

### 2. Tool Call ID 格式

```
Anthropic: toolu_01XFDzDj...（toolu_ 前缀）
OpenAI:    call_abc123    （call_ 前缀）
```

大多数代理直接透传（CC 不校验前缀格式），也可做双向映射（更安全但需要状态）。

### 3. 流式 Tool Call 拼装

Chat Completions 的 tool call 跨多个 chunk 增量传输：

```json
// chunk 1：开始，带 name
{"delta": {"tool_calls": [{"index": 0, "id": "call_abc", "function": {"name": "Read"}}]}}
// chunk 2~N：参数 delta
{"delta": {"tool_calls": [{"index": 0, "function": {"arguments": "{\"file"}}]}}
```

状态机需跟踪：哪个 index 已开始 → 发 `content_block_start`；参数 delta → 转发 `content_block_delta`；新 index 出现 → 关闭上一个 block，打开新 block。

> `maxnowack/anthropic-proxy` 的 bug #5 就在这里——多工具时块索引错乱。

### 4. Content Block 顺序

- Anthropic：扁平数组 `[thinking, text, tool_use, tool_use]`
- ChatCompletions：`content` + `reasoning_content` + `tool_calls`（分开的字段）

流式必须按顺序发出：thinking → text → tool_use，并正确处理 `content_block_stop` / `content_block_start` 转换。

### 5. Claude Code 特有行为

- **BatchTool**：CC 可能发送 batch tool 定义，大多数 provider 拒绝 → 过滤掉
- **`format: "uri"` in JSON Schema**：CC 工具 schema 包含这个，Google 等 provider 报错 → strip
- **空 `properties: {}`**：部分 provider 要求 object schema 有 properties → 补全
- **超大 system prompt**：CC 的 system prompt 包含大量工具定义，可能超出 provider 限制
- **高频多工具调用**：CC 每轮可能调用 3-5 个工具，连接可靠性很重要

### 6. Thinking / Extended Thinking

不同 provider 支持程度不同：
- OpenAI o1/o3：有 `reasoning_content`（非标准扩展）
- DeepSeek：也用 `reasoning_content`
- 大多数 provider：完全不支持

如果 provider 不支持 → thinking 配置被静默忽略，模型行为改变。

### 7. 工具名长度

OpenAI 函数名 64 字符限制，Anthropic 无此限制。LiteLLM 用 `{55字符前缀}_{8字符sha256}` 截断 + 还原映射。轻量代理通常跳过 → CC 发长工具名时会报错。

## 推荐路线

### 短期：用 LiteLLM 验证可行性

```yaml
# litellm config.yaml
model_list:
  - model_name: claude-sonnet-4-20250514
    litellm_params:
      model: openai/gpt-4.1
      api_key: sk-xxx
      api_base: https://your-provider.com/v1
```

```bash
litellm --config config.yaml --port 4000
# Claude Code: ANTHROPIC_BASE_URL=http://localhost:4000
```

快速验证该 ChatCompletions 后端是否真的支持 CC 的 agentic 工作流。

### 中期：自研 Go 实现（方案 B，直接转换）

~500-800 行，参考：
- LiteLLM `streaming_iterator.py`（488 行）— 最完整的流式状态机
- sub2api `apicompat/` — Go 同类问题的实现模式
- anthropic-proxy-rs — 最整洁的轻量实现
