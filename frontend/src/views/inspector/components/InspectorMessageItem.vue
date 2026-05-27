<template>
  <div
    :id="`msg-${idx}`"
    ref="elRef"
    class="inspector-msg rounded-lg border p-3 transition-shadow"
    :class="[
      flash ? 'ring-2 ring-blue-500' : '',
      'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700'
    ]"
  >
    <!-- Header -->
    <div class="mb-1.5 flex flex-wrap items-center gap-2 text-xs">
      <span
        class="rounded-full px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide"
        :class="roleBadgeClass"
      >{{ role }}</span>

      <span v-if="message.name" class="font-mono text-gray-500 dark:text-gray-400">
        name={{ message.name }}
      </span>
      <span v-if="message.tool_call_id" class="font-mono text-gray-500 dark:text-gray-400">
        tool_call_id={{ message.tool_call_id }}
      </span>
      <span
        v-if="reasoning"
        class="rounded bg-purple-100 px-1.5 py-0.5 font-mono text-purple-700 dark:bg-purple-900/30 dark:text-purple-300"
        title="has reasoning trace"
      >
        reasoning {{ reasoning.length }} chars
      </span>
      <span
        v-if="isMultimodal"
        class="rounded bg-amber-100 px-1.5 py-0.5 font-mono text-amber-700 dark:bg-amber-900/30 dark:text-amber-300"
      >
        {{ countBlocks() }}
      </span>
      <span class="ml-auto font-mono text-gray-400 dark:text-gray-500">
        #{{ idx }} · {{ textContent.length }} chars
      </span>
    </div>

    <!-- Reasoning block -->
    <div
      v-if="reasoning"
      class="mb-2 overflow-hidden rounded border-l-[3px] border-purple-500 bg-purple-50/50 dark:bg-purple-900/10"
    >
      <div
        class="flex cursor-pointer items-center gap-2 bg-purple-100/50 px-3 py-1 text-xs select-none hover:bg-purple-100 dark:bg-purple-900/20 dark:hover:bg-purple-900/30"
        @click="reasoningOpen = !reasoningOpen"
      >
        <span class="font-semibold text-purple-600 dark:text-purple-300">reasoning</span>
        <span class="text-gray-500 dark:text-gray-400">
          {{ reasoning.length }} chars · click to {{ reasoningOpen ? 'collapse' : 'expand' }}
        </span>
      </div>
      <div class="max-h-[360px] overflow-auto whitespace-pre-wrap px-3 py-1.5 font-mono text-xs italic leading-relaxed text-purple-700 break-all dark:text-purple-300">
        {{ reasoningOpen ? reasoning : reasoningPreview }}
      </div>
    </div>

    <!-- Content -->
    <div v-if="isMultimodal" class="space-y-2">
      <ContentBlockView v-for="(block, j) in contentBlocks" :key="j" :block="block" />
    </div>
    <div v-else>
      <div
        class="whitespace-pre-wrap font-mono text-xs leading-relaxed text-gray-800 break-all dark:text-gray-200"
        :class="{ 'max-h-[100px] overflow-hidden': !expanded && displayContent.length >= 1000 }"
      >
        {{ displayContent || '(empty)' }}
      </div>
      <button
        v-if="displayContent.length >= 1000"
        class="mt-1 text-xs text-blue-500 hover:text-blue-700 dark:text-blue-400"
        @click="expanded = !expanded"
      >
        [{{ expanded ? 'collapse' : `expand ${displayContent.length} chars` }}]
      </button>
      <details v-if="hiddenContent" class="mt-1.5">
        <summary class="cursor-pointer text-[11px] text-gray-500 dark:text-gray-400">
          Full message ({{ hiddenContent.length }} chars, including system context)
        </summary>
        <div class="mt-1 max-h-[400px] overflow-auto whitespace-pre-wrap font-mono text-xs text-gray-700 break-all dark:text-gray-300">
          {{ hiddenContent }}
        </div>
      </details>
    </div>

    <!-- Tool calls -->
    <div v-if="message.tool_calls?.length" class="mt-2 space-y-1.5">
      <ToolCallBlock v-for="(tc, j) in message.tool_calls" :key="j" :tc="tc" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
import { contentToText, extractUserText, type NormalizedMessage, type ContentBlock } from '../lib/messages'
import ContentBlockView from './InspectorContentBlock.vue'
import ToolCallBlock from './InspectorToolCallBlock.vue'

const props = defineProps<{
  message: NormalizedMessage
  idx: number
  highlight?: boolean
}>()

const elRef = ref<HTMLElement | null>(null)
const flash = ref(false)
const expanded = ref(false)
const reasoningOpen = ref(false)

const role = computed(() => props.message.role || 'unknown')
const rawContent = computed(() => props.message.content)
const isMultimodal = computed(() => Array.isArray(rawContent.value))
const contentBlocks = computed(() => (isMultimodal.value ? (rawContent.value as ContentBlock[]) : []))
const textContent = computed(() => contentToText(rawContent.value))
const reasoning = computed(() => props.message.reasoning_content ?? null)
const reasoningPreview = computed(() => {
  const r = reasoning.value
  if (!r) return ''
  return r.length > 240 ? r.slice(0, 240) + '…' : r
})

const displayContent = computed(() => {
  if (role.value === 'user' && !isMultimodal.value) {
    const stripped = extractUserText(rawContent.value)
    if (stripped !== textContent.value.trim()) return stripped
  }
  return textContent.value
})

const hiddenContent = computed(() => {
  if (role.value === 'user' && !isMultimodal.value) {
    const stripped = extractUserText(rawContent.value)
    if (stripped !== textContent.value.trim()) return textContent.value
  }
  return null
})

const roleBadgeClass = computed(() => {
  switch (role.value) {
    case 'system': return 'bg-gray-600 text-white'
    case 'user': return 'bg-blue-600 text-white'
    case 'assistant': return 'bg-green-600 text-white'
    case 'tool': return 'bg-purple-600 text-white'
    case 'tool_call': return 'bg-amber-600 text-white'
    default: return 'bg-gray-500 text-white'
  }
})

function countBlocks(): string {
  if (!isMultimodal.value) return ''
  let txt = 0, img = 0, other = 0
  for (const b of contentBlocks.value) {
    if (!b || typeof b !== 'object') continue
    if (b.type === 'text') txt++
    else if (b.type === 'image_url') img++
    else other++
  }
  const parts: string[] = []
  if (txt) parts.push(`${txt} text`)
  if (img) parts.push(`${img} image`)
  if (other) parts.push(`${other} other`)
  return parts.join(' + ') || '0 blocks'
}

watch(
  () => props.highlight,
  (val) => {
    if (val && elRef.value) {
      nextTick(() => {
        elRef.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
        flash.value = true
        setTimeout(() => (flash.value = false), 1200)
      })
    }
  }
)
</script>
