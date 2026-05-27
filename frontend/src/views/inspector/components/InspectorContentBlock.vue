<template>
  <div v-if="block.type === 'text'">
    <div
      class="whitespace-pre-wrap font-mono text-xs leading-relaxed text-gray-800 break-all dark:text-gray-200"
      :class="{ 'max-h-[100px] overflow-hidden': !expanded && text.length >= 1000 }"
    >
      {{ text || '(empty text block)' }}
    </div>
    <button
      v-if="text.length >= 1000"
      class="mt-1 text-xs text-blue-500 hover:text-blue-700 dark:text-blue-400"
      @click="expanded = !expanded"
    >
      [{{ expanded ? 'collapse' : `expand ${text.length} chars` }}]
    </button>
  </div>

  <div v-else-if="block.type === 'image_url'" class="inline-flex flex-col rounded border border-gray-200 bg-gray-50 p-1.5 dark:border-gray-700 dark:bg-gray-900">
    <img
      v-if="imageUrl && /^(https?:|data:)/.test(imageUrl)"
      :src="imageUrl"
      alt="multimodal content"
      class="block max-h-[480px] max-w-[640px] rounded bg-black"
      loading="lazy"
    />
    <div v-else class="font-mono text-[11px] text-gray-500 dark:text-gray-400">
      [image_url] {{ shortenUrl(imageUrl) }}
    </div>
  </div>

  <div v-else-if="block.type === 'tool_use'" class="rounded border-l-[3px] border-amber-500 bg-amber-50 px-3 py-1.5 font-mono text-xs dark:bg-amber-900/10">
    <span class="font-semibold text-amber-700 dark:text-amber-300">{{ (block as any).name ?? '?' }}</span>
    <span v-if="(block as any).id" class="ml-2 text-gray-500 dark:text-gray-400">id={{ (block as any).id }}</span>
    <pre class="mt-1 whitespace-pre-wrap break-all">{{ formatToolInput((block as any).input) }}</pre>
  </div>

  <div v-else-if="block.type === 'thinking'" class="rounded border-l-[3px] border-purple-500 bg-purple-50/50 px-3 py-1.5 font-mono text-xs italic text-purple-700 dark:bg-purple-900/10 dark:text-purple-300">
    {{ (block as any).thinking ?? '' }}
  </div>

  <div v-else class="font-mono text-xs text-gray-500 dark:text-gray-400">
    <span>[{{ block.type ?? 'unknown' }} block]</span>
    <pre class="mt-1 text-[11px]">{{ JSON.stringify(block, null, 2) }}</pre>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import type { ContentBlock } from '../lib/messages'

const props = defineProps<{ block: ContentBlock }>()
const expanded = ref(false)

const text = computed(() => (props.block as { text?: string }).text ?? '')
const imageUrl = computed(() => (props.block as { image_url?: { url: string } }).image_url?.url ?? '')

function shortenUrl(u: string): string {
  if (u.length <= 80) return u
  return u.slice(0, 40) + '…' + u.slice(-30)
}

function formatToolInput(input: unknown): string {
  if (typeof input === 'string') return input
  return JSON.stringify(input, null, 2)
}
</script>
