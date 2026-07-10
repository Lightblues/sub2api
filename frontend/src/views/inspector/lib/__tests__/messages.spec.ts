import { describe, it, expect } from 'vitest'
import { normalizeMessages, contentToText, extractResponseOutput } from '../messages'

describe('normalizeMessages', () => {
  describe('OpenAI ChatCompletions', () => {
    it('returns messages as-is and prepends system if present', () => {
      const out = normalizeMessages({
        system: 'sys-prompt',
        messages: [
          { role: 'user', content: 'hi' },
          { role: 'assistant', content: 'hello' }
        ]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(3)
      expect(out![0]).toEqual({ role: 'system', content: 'sys-prompt' })
      expect(out![1].role).toBe('user')
      expect(out![2].role).toBe('assistant')
    })

    it('does not detect Anthropic format for plain text content', () => {
      const out = normalizeMessages({
        messages: [{ role: 'user', content: 'hi' }]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(1)
    })
  })

  describe('Anthropic Messages', () => {
    it('extracts thinking as reasoning_content and tool_use as tool_calls', () => {
      const out = normalizeMessages({
        system: 'you are helpful',
        messages: [
          { role: 'user', content: 'find weather' },
          {
            role: 'assistant',
            content: [
              { type: 'thinking', thinking: 'let me think' },
              { type: 'text', text: 'I will call a tool' },
              { type: 'tool_use', id: 'tu_1', name: 'get_weather', input: { city: 'SF' } }
            ]
          },
          {
            role: 'user',
            content: [{ type: 'tool_result', tool_use_id: 'tu_1', content: 'sunny' }]
          }
        ]
      })
      expect(out).not.toBeNull()
      // [system, user, assistant, tool]
      expect(out!.length).toBe(4)
      expect(out![0].role).toBe('system')
      expect(out![2].role).toBe('assistant')
      expect(out![2].reasoning_content).toBe('let me think')
      expect(out![2].content).toBe('I will call a tool')
      expect(out![2].tool_calls).toBeDefined()
      expect(out![2].tool_calls![0].function!.name).toBe('get_weather')
      expect(out![2].tool_calls![0].function!.arguments).toBe('{"city":"SF"}')
      expect(out![3].role).toBe('tool')
      expect(out![3].tool_call_id).toBe('tu_1')
      expect(out![3].content).toBe('sunny')
    })
  })

  describe('OpenAI Responses API', () => {
    it('treats string input as a user message and adds instructions as system', () => {
      const out = normalizeMessages({
        instructions: 'be brief',
        input: 'tell me a joke'
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(2)
      expect(out![0]).toEqual({ role: 'system', content: 'be brief' })
      expect(out![1]).toEqual({ role: 'user', content: 'tell me a joke' })
    })

    it('handles role-based message items with input_text content parts', () => {
      const out = normalizeMessages({
        input: [
          {
            type: 'message',
            role: 'user',
            content: [
              { type: 'input_text', text: 'part 1' },
              { type: 'input_text', text: 'part 2' }
            ]
          }
        ]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(1)
      expect(out![0].role).toBe('user')
      // all-text parts collapse to a string
      expect(out![0].content).toBe('part 1\npart 2')
    })

    it('handles input_image parts as image_url blocks', () => {
      const out = normalizeMessages({
        input: [
          {
            type: 'message',
            role: 'user',
            content: [
              { type: 'input_text', text: 'describe' },
              { type: 'input_image', image_url: 'https://example.com/img.png' }
            ]
          }
        ]
      })
      expect(out).not.toBeNull()
      expect(Array.isArray(out![0].content)).toBe(true)
      const blocks = out![0].content as Array<Record<string, unknown>>
      expect(blocks).toHaveLength(2)
      expect(blocks[0]).toEqual({ type: 'text', text: 'describe' })
      expect(blocks[1]).toEqual({ type: 'image_url', image_url: { url: 'https://example.com/img.png' } })
    })

    it('translates function_call into assistant.tool_calls', () => {
      const out = normalizeMessages({
        input: [
          { role: 'user', content: 'list dir' },
          {
            type: 'function_call',
            call_id: 'call_abc',
            name: 'list_dir',
            arguments: '{"path":"/tmp"}'
          }
        ]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(2)
      expect(out![1].role).toBe('assistant')
      expect(out![1].tool_calls).toHaveLength(1)
      expect(out![1].tool_calls![0]).toEqual({
        id: 'call_abc',
        type: 'function',
        function: { name: 'list_dir', arguments: '{"path":"/tmp"}' }
      })
    })

    it('merges multiple consecutive function_calls into the same assistant turn', () => {
      const out = normalizeMessages({
        input: [
          { role: 'user', content: 'do stuff' },
          { type: 'function_call', call_id: 'c1', name: 'f1', arguments: '{}' },
          { type: 'function_call', call_id: 'c2', name: 'f2', arguments: '{}' }
        ]
      })
      expect(out).not.toBeNull()
      // [user, assistant w/ 2 tool_calls]
      expect(out!.length).toBe(2)
      expect(out![1].tool_calls).toHaveLength(2)
      expect(out![1].tool_calls!.map((t) => t.id)).toEqual(['c1', 'c2'])
    })

    it('translates function_call_output into role=tool messages', () => {
      const out = normalizeMessages({
        input: [
          { type: 'function_call', call_id: 'c1', name: 'f', arguments: '{}' },
          { type: 'function_call_output', call_id: 'c1', output: 'done' }
        ]
      })
      expect(out).not.toBeNull()
      // [assistant tool_calls, tool result]
      expect(out!.length).toBe(2)
      expect(out![1]).toEqual({ role: 'tool', tool_call_id: 'c1', content: 'done' })
    })

    it('attaches reasoning summary to the next assistant message', () => {
      const out = normalizeMessages({
        input: [
          { role: 'user', content: 'why?' },
          {
            type: 'reasoning',
            summary: [{ type: 'summary_text', text: 'thinking step' }]
          },
          { role: 'assistant', content: 'because' }
        ]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(2)
      expect(out![1].role).toBe('assistant')
      expect(out![1].reasoning_content).toBe('thinking step')
      expect(out![1].content).toBe('because')
    })

    it('attaches reasoning to the next assistant tool_calls turn when there is no plain assistant text', () => {
      const out = normalizeMessages({
        input: [
          { role: 'user', content: 'go' },
          {
            type: 'reasoning',
            summary: [{ type: 'summary_text', text: 'plan' }]
          },
          { type: 'function_call', call_id: 'c1', name: 'f', arguments: '{}' }
        ]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(2)
      expect(out![1].role).toBe('assistant')
      expect(out![1].tool_calls).toHaveLength(1)
      expect(out![1].reasoning_content).toBe('plan')
    })

    it('surfaces encrypted reasoning as an opaque marker', () => {
      const out = normalizeMessages({
        input: [
          { type: 'reasoning', encrypted_content: 'x'.repeat(123) },
          { role: 'assistant', content: 'ok' }
        ]
      })
      expect(out).not.toBeNull()
      const a = out!.find((m) => m.role === 'assistant')!
      expect(a.reasoning_content).toContain('[encrypted reasoning: 123 chars]')
    })

    it('emits trailing reasoning as its own assistant message', () => {
      const out = normalizeMessages({
        input: [
          { role: 'user', content: 'q' },
          { type: 'reasoning', summary: [{ type: 'summary_text', text: 'pondering' }] }
        ]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(2)
      expect(out![1]).toEqual({ role: 'assistant', reasoning_content: 'pondering' })
    })

    it('surfaces unknown item types as a system block (so nothing is silently dropped)', () => {
      const out = normalizeMessages({
        input: [{ type: 'mystery_item', foo: 'bar' }]
      })
      expect(out).not.toBeNull()
      expect(out!.length).toBe(1)
      expect(out![0].role).toBe('system')
      expect(typeof out![0].content === 'string' && out![0].content.startsWith('[mystery_item]')).toBe(true)
    })

    it('end-to-end: realistic Codex-style turn (user → reasoning → fn_call → fn_output → assistant)', () => {
      const out = normalizeMessages({
        instructions: 'You are Codex.',
        input: [
          {
            type: 'message',
            role: 'user',
            content: [{ type: 'input_text', text: 'list /tmp' }]
          },
          {
            type: 'reasoning',
            summary: [{ type: 'summary_text', text: 'I should call list_dir' }]
          },
          {
            type: 'function_call',
            call_id: 'call_1',
            name: 'list_dir',
            arguments: '{"path":"/tmp"}'
          },
          {
            type: 'function_call_output',
            call_id: 'call_1',
            output: 'a.txt\nb.txt'
          },
          {
            type: 'message',
            role: 'assistant',
            content: [{ type: 'output_text', text: 'There are two files: a.txt and b.txt.' }]
          }
        ]
      })
      expect(out).not.toBeNull()
      const roles = out!.map((m) => m.role)
      // [system instructions, user, assistant(tool_call, reasoning), tool, assistant final]
      expect(roles).toEqual(['system', 'user', 'assistant', 'tool', 'assistant'])
      expect(out![0].content).toBe('You are Codex.')
      expect(out![1].content).toBe('list /tmp')
      expect(out![2].reasoning_content).toBe('I should call list_dir')
      expect(out![2].tool_calls).toHaveLength(1)
      expect(out![2].tool_calls![0].function!.name).toBe('list_dir')
      expect(out![3]).toEqual({ role: 'tool', tool_call_id: 'call_1', content: 'a.txt\nb.txt' })
      expect(out![4].role).toBe('assistant')
      expect(out![4].content).toBe('There are two files: a.txt and b.txt.')
    })

    it('returns null for an empty body without messages or input', () => {
      expect(normalizeMessages({})).toBeNull()
    })
  })
})

describe('contentToText (Responses parts)', () => {
  it('flattens input_text/output_text and marks input_image', () => {
    const text = contentToText([
      { type: 'input_text', text: 'a' },
      { type: 'output_text', text: 'b' },
      { type: 'input_image', image_url: 'https://example.com/x.png' } as any
    ])
    expect(text).toBe('a\nb\n[image]')
  })
})

describe('extractResponseOutput (Responses)', () => {
  it('extracts assistant text + reasoning from response_complete.response.output[]', () => {
    const out = extractResponseOutput({
      response_complete: {
        response: {
          output: [
            {
              type: 'reasoning',
              summary: [{ type: 'summary_text', text: 'thought' }]
            },
            {
              type: 'message',
              role: 'assistant',
              content: [{ type: 'output_text', text: 'final answer' }]
            }
          ]
        }
      }
    })
    expect(out).toHaveLength(2)
    expect(out[0].reasoning_content).toBe('thought')
    expect(out[1].content).toBe('final answer')
  })
})
