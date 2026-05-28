/**
 * Message content types and helpers for the Inspector chat visualization.
 * Supports both OpenAI and Anthropic API formats.
 */

export type ContentBlock =
  | { type: 'text'; text: string }
  | { type: 'image_url'; image_url: { url: string } }
  | { type: 'tool_use'; id: string; name: string; input: unknown }
  | { type: 'tool_result'; tool_use_id: string; content: unknown }
  | { type: 'thinking'; thinking: string }
  // OpenAI Responses API content parts (kept as-is; rendered in InspectorContentBlock)
  | { type: 'input_text'; text: string }
  | { type: 'output_text'; text: string }
  | { type: 'input_image'; image_url: string }
  | { type: string; [k: string]: unknown }

export type MessageContent = string | ContentBlock[] | null | undefined

export interface ToolCall {
  id?: string
  type?: string
  function?: { name?: string; arguments?: string }
  name?: string
  arguments?: string | object
}

export interface NormalizedMessage {
  role: string
  content?: MessageContent
  reasoning_content?: string | null
  tool_calls?: ToolCall[]
  tool_call_id?: string
  name?: string
}

const USER_QUERY_RE = /<user_query>([\s\S]*?)<\/user_query>/

export function contentToText(content: MessageContent): string {
  if (content == null) return ''
  if (typeof content === 'string') return content
  if (!Array.isArray(content)) return ''
  const parts: string[] = []
  for (const b of content) {
    if (!b || typeof b !== 'object') continue
    if (
      (b.type === 'text' || b.type === 'input_text' || b.type === 'output_text') &&
      typeof (b as { text?: string }).text === 'string'
    ) {
      parts.push((b as { text: string }).text)
    } else if (b.type === 'image_url' || b.type === 'input_image') {
      parts.push('[image]')
    } else if (b.type === 'tool_use') {
      const tu = b as { name?: string }
      parts.push(`[tool_use: ${tu.name ?? '?'}]`)
    } else if (b.type === 'tool_result') {
      parts.push('[tool_result]')
    } else if (b.type === 'thinking') {
      parts.push('[thinking]')
    }
  }
  return parts.join('\n')
}

export function extractUserText(content: MessageContent): string {
  const s = contentToText(content)
  if (!s) return ''
  const m = USER_QUERY_RE.exec(s)
  return (m ? m[1] : s).trim()
}

export function summarize(content: string | null | undefined, max = 100): string {
  const s = (content ?? '').replace(/\s+/g, ' ').trim()
  return s.length > max ? s.slice(0, max) + '…' : s
}

/**
 * Extract the model response output from the raw record's response_complete field.
 * Handles OpenAI Responses API format: response_complete.response.output[]
 * and ChatCompletions format: response_body.choices[].message
 */
export function extractResponseOutput(record: Record<string, unknown>): NormalizedMessage[] {
  const result: NormalizedMessage[] = []

  // OpenAI Responses API: response_complete.response.output
  const rc = record.response_complete as Record<string, unknown> | undefined
  if (rc) {
    const resp = rc.response as Record<string, unknown> | undefined
    if (resp) {
      const output = resp.output as Array<Record<string, unknown>> | undefined
      if (Array.isArray(output)) {
        for (const item of output) {
          if (item.type === 'message' && item.role === 'assistant') {
            const content = item.content as Array<Record<string, unknown>> | undefined
            if (Array.isArray(content)) {
              const textParts = content
                .filter((c) => c.type === 'output_text' || c.type === 'text')
                .map((c) => (c.text as string) ?? '')
              if (textParts.length > 0) {
                result.push({ role: 'assistant', content: textParts.join('\n') })
              }
            }
          } else if (item.type === 'reasoning') {
            const summary = item.summary as Array<Record<string, unknown>> | undefined
            if (Array.isArray(summary)) {
              const text = summary
                .filter((s) => s.type === 'summary_text')
                .map((s) => (s.text as string) ?? '')
                .join('\n')
              if (text) {
                result.push({ role: 'assistant', reasoning_content: text })
              }
            }
          }
        }
      }
    }
  }

  // ChatCompletions format: response_body.choices[].message
  if (result.length === 0) {
    const rb = record.response_body as Record<string, unknown> | undefined
    if (rb) {
      const choices = rb.choices as Array<Record<string, unknown>> | undefined
      if (Array.isArray(choices)) {
        for (const choice of choices) {
          const msg = choice.message as Record<string, unknown> | undefined
          if (msg && msg.role === 'assistant') {
            const nm: NormalizedMessage = {
              role: 'assistant',
              content: (msg.content as string) ?? undefined,
              reasoning_content: (msg.reasoning_content as string) ?? null,
              tool_calls: msg.tool_calls as NormalizedMessage['tool_calls']
            }
            result.push(nm)
          }
        }
      }
    }
  }

  // Anthropic Messages format: response_complete has content[] directly (or via response_body)
  if (result.length === 0 && rc) {
    const content = rc.content as Array<Record<string, unknown>> | undefined
    if (Array.isArray(content)) {
      const textParts = content
        .filter((c) => c.type === 'text')
        .map((c) => (c.text as string) ?? '')
      const thinkingParts = content
        .filter((c) => c.type === 'thinking')
        .map((c) => (c.thinking as string) ?? '')
      if (textParts.length > 0 || thinkingParts.length > 0) {
        result.push({
          role: 'assistant',
          content: textParts.join('\n') || undefined,
          reasoning_content: thinkingParts.length > 0 ? thinkingParts.join('\n') : null
        })
      }
    }
  }

  return result
}

/**
 * Detect if a request body uses Anthropic format and normalize messages
 * to a unified shape for the ChatView component.
 *
 * Supported request shapes:
 *  - OpenAI ChatCompletions: { messages: [...] }
 *  - Anthropic Messages:     { system?, messages: [...with tool_use/tool_result/thinking blocks] }
 *  - OpenAI Responses:       { instructions?, input: string | [...input items] }
 */
export function normalizeMessages(requestBody: Record<string, unknown>): NormalizedMessage[] | null {
  if (!requestBody || typeof requestBody !== 'object') return null

  // OpenAI Responses API: top-level "input" instead of "messages".
  if (!Array.isArray(requestBody.messages) && (typeof requestBody.input === 'string' || Array.isArray(requestBody.input))) {
    return normalizeResponsesInput(requestBody)
  }

  const messages = requestBody?.messages
  if (!Array.isArray(messages)) return null

  // Detect Anthropic format by checking for content blocks with tool_use/tool_result types
  const isAnthropic = messages.some(
    (m: Record<string, unknown>) =>
      Array.isArray(m.content) &&
      (m.content as ContentBlock[]).some(
        (b) => b.type === 'tool_use' || b.type === 'tool_result' || b.type === 'thinking'
      )
  )

  if (!isAnthropic) {
    // OpenAI format — return as-is with system message prepended if present
    const result: NormalizedMessage[] = []
    if (typeof requestBody.system === 'string' && requestBody.system) {
      result.push({ role: 'system', content: requestBody.system })
    }
    for (const m of messages) {
      result.push(m as NormalizedMessage)
    }
    return result
  }

  // Anthropic format normalization
  const result: NormalizedMessage[] = []

  // Anthropic system can be string or array of content blocks
  const sys = requestBody.system
  if (typeof sys === 'string' && sys) {
    result.push({ role: 'system', content: sys })
  } else if (Array.isArray(sys)) {
    const text = (sys as ContentBlock[])
      .filter((b) => b.type === 'text')
      .map((b) => (b as { text: string }).text)
      .join('\n')
    if (text) result.push({ role: 'system', content: text })
  }

  for (const m of messages as Record<string, unknown>[]) {
    const role = m.role as string
    const content = m.content

    if (role === 'assistant' && Array.isArray(content)) {
      const blocks = content as ContentBlock[]

      // Extract thinking blocks as reasoning_content
      const thinkingParts = blocks
        .filter((b) => b.type === 'thinking')
        .map((b) => (b as { thinking: string }).thinking)
      const reasoning = thinkingParts.length > 0 ? thinkingParts.join('\n') : null

      // Extract text blocks
      const textParts = blocks
        .filter((b) => b.type === 'text')
        .map((b) => (b as { text: string }).text)

      // Extract tool_use blocks as tool_calls
      const toolUses = blocks.filter((b) => b.type === 'tool_use') as Array<{
        type: 'tool_use'
        id: string
        name: string
        input: unknown
      }>
      const toolCalls: ToolCall[] = toolUses.map((tu) => ({
        id: tu.id,
        type: 'function',
        function: {
          name: tu.name,
          arguments: typeof tu.input === 'string' ? tu.input : JSON.stringify(tu.input)
        }
      }))

      result.push({
        role: 'assistant',
        content: textParts.join('\n') || undefined,
        reasoning_content: reasoning,
        tool_calls: toolCalls.length > 0 ? toolCalls : undefined
      })
    } else if (role === 'user' && Array.isArray(content)) {
      const blocks = content as ContentBlock[]

      // Check for tool_result blocks
      const toolResults = blocks.filter((b) => b.type === 'tool_result') as Array<{
        type: 'tool_result'
        tool_use_id: string
        content: unknown
      }>

      if (toolResults.length > 0) {
        // Each tool_result becomes a separate "tool" role message
        for (const tr of toolResults) {
          const trContent =
            typeof tr.content === 'string'
              ? tr.content
              : Array.isArray(tr.content)
                ? (tr.content as ContentBlock[])
                    .filter((b) => b.type === 'text')
                    .map((b) => (b as { text: string }).text)
                    .join('\n')
                : JSON.stringify(tr.content)
          result.push({
            role: 'tool',
            tool_call_id: tr.tool_use_id,
            content: trContent
          })
        }

        // Also extract any non-tool_result content blocks as user message
        const otherBlocks = blocks.filter((b) => b.type !== 'tool_result')
        if (otherBlocks.length > 0) {
          result.push({ role: 'user', content: otherBlocks })
        }
      } else {
        result.push({ role: 'user', content: content as MessageContent })
      }
    } else {
      result.push({ role, content: content as MessageContent })
    }
  }

  return result
}

// ---------------------------------------------------------------------------
// OpenAI Responses API request normalization
// ---------------------------------------------------------------------------

/**
 * Normalize a Responses API request body to a unified message list.
 *
 * Responses input items can be:
 *   - role-based message: { role, content: string | ResponsesContentPart[] }
 *     where part.type ∈ {input_text, output_text, input_image}
 *   - { type: "function_call", call_id, name, arguments }
 *     -> rendered as an assistant message with tool_calls (also flushes pending assistant text)
 *   - { type: "function_call_output", call_id, output }
 *     -> rendered as a "tool" role message
 *   - { type: "reasoning", summary: [{type:"summary_text", text}], encrypted_content? }
 *     -> attached as reasoning_content to the next assistant message (or its own assistant message)
 *
 * `instructions` (top-level) is treated as a system message if present.
 */
function normalizeResponsesInput(requestBody: Record<string, unknown>): NormalizedMessage[] {
  const result: NormalizedMessage[] = []

  const instructions = requestBody.instructions
  if (typeof instructions === 'string' && instructions) {
    result.push({ role: 'system', content: instructions })
  }

  const input = requestBody.input
  if (typeof input === 'string') {
    if (input) result.push({ role: 'user', content: input })
    return result
  }
  if (!Array.isArray(input)) return result

  let pendingReasoning: string | null = null

  for (const raw of input as Array<Record<string, unknown>>) {
    if (!raw || typeof raw !== 'object') continue
    const type = (raw.type as string) || ''

    // Role-based message ("" type or explicit role)
    if (!type || type === 'message') {
      const role = (raw.role as string) || 'user'
      const content = normalizeResponsesContent(raw.content)
      const msg: NormalizedMessage = { role, content }
      if (role === 'assistant' && pendingReasoning) {
        msg.reasoning_content = pendingReasoning
        pendingReasoning = null
      }
      result.push(msg)
      continue
    }

    if (type === 'function_call') {
      const callId = (raw.call_id as string) || (raw.id as string) || ''
      const name = (raw.name as string) || ''
      const args = (raw.arguments as string) || ''
      const toolCall: ToolCall = {
        id: callId,
        type: 'function',
        function: { name, arguments: args }
      }
      // Try to merge with the previous assistant message if it has no tool_calls yet
      // (Responses streams may emit text + function_call in the same logical turn).
      const last = result[result.length - 1]
      if (last && last.role === 'assistant' && !last.tool_calls) {
        last.tool_calls = [toolCall]
      } else if (last && last.role === 'assistant' && last.tool_calls) {
        last.tool_calls.push(toolCall)
      } else {
        const msg: NormalizedMessage = { role: 'assistant', tool_calls: [toolCall] }
        if (pendingReasoning) {
          msg.reasoning_content = pendingReasoning
          pendingReasoning = null
        }
        result.push(msg)
      }
      continue
    }

    if (type === 'function_call_output') {
      const callId = (raw.call_id as string) || ''
      const output = (raw.output as string) || ''
      result.push({ role: 'tool', tool_call_id: callId, content: output })
      continue
    }

    if (type === 'reasoning') {
      const summary = raw.summary as Array<Record<string, unknown>> | undefined
      let text = ''
      if (Array.isArray(summary)) {
        text = summary
          .filter((s) => s.type === 'summary_text' && typeof s.text === 'string')
          .map((s) => s.text as string)
          .join('\n')
      }
      // Buffer reasoning to attach onto the next assistant message
      if (text) {
        pendingReasoning = pendingReasoning ? pendingReasoning + '\n' + text : text
      } else if (typeof raw.encrypted_content === 'string' && raw.encrypted_content) {
        // Surface encrypted reasoning as an opaque marker so the user knows it exists
        const marker = `[encrypted reasoning: ${(raw.encrypted_content as string).length} chars]`
        pendingReasoning = pendingReasoning ? pendingReasoning + '\n' + marker : marker
      }
      continue
    }

    // Unknown item type — surface as a raw block so it's still visible
    result.push({ role: 'system', content: `[${type}]\n` + JSON.stringify(raw, null, 2) })
  }

  // If we had trailing reasoning with no following assistant message, emit it as one.
  if (pendingReasoning) {
    result.push({ role: 'assistant', reasoning_content: pendingReasoning })
  }

  return result
}

/**
 * Convert a Responses API message content (string or content-part array) to the
 * unified MessageContent shape used by NormalizedMessage.
 *
 * input_text/output_text -> {type: 'text', text}
 * input_image            -> {type: 'image_url', image_url: {url}}
 * unknown                -> kept as-is (rendered via the generic branch)
 */
function normalizeResponsesContent(raw: unknown): MessageContent {
  if (raw == null) return ''
  if (typeof raw === 'string') return raw
  if (!Array.isArray(raw)) return ''

  const blocks: ContentBlock[] = []
  for (const part of raw as Array<Record<string, unknown>>) {
    if (!part || typeof part !== 'object') continue
    const t = part.type as string
    if (t === 'input_text' || t === 'output_text' || t === 'text') {
      const text = typeof part.text === 'string' ? part.text : ''
      blocks.push({ type: 'text', text })
    } else if (t === 'input_image') {
      const url = typeof part.image_url === 'string' ? part.image_url : ''
      blocks.push({ type: 'image_url', image_url: { url } })
    } else {
      blocks.push({ type: t || 'unknown', ...part } as ContentBlock)
    }
  }

  // Collapse to a plain string when every block is text — it renders nicer.
  if (blocks.every((b) => b.type === 'text')) {
    return blocks.map((b) => (b as { text: string }).text).join('\n')
  }
  return blocks
}
