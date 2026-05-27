<template>
  <div class="rounded border-l-[3px] border-amber-500 bg-amber-50 px-3 py-1.5 font-mono text-xs dark:bg-amber-900/10">
    <span class="font-semibold text-amber-700 dark:text-amber-300">{{ name }}</span>
    <span v-if="tc.id" class="ml-2 text-gray-500 dark:text-gray-400">id={{ tc.id }}</span>
    <pre class="mt-1 whitespace-pre-wrap break-all">{{ open ? args : preview }}</pre>
    <button
      v-if="args.length > 200"
      class="mt-0.5 text-blue-500 hover:text-blue-700 dark:text-blue-400"
      @click="open = !open"
    >
      [{{ open ? 'collapse' : 'expand' }}]
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import type { ToolCall } from '../lib/messages'

const props = defineProps<{ tc: ToolCall }>()
const open = ref(false)

const name = computed(() => props.tc.function?.name ?? props.tc.name ?? '?')
const args = computed(() => {
  const raw = props.tc.function?.arguments ?? props.tc.arguments ?? ''
  return typeof raw === 'string' ? raw : JSON.stringify(raw, null, 2)
})
const preview = computed(() => (args.value.length > 200 ? args.value.slice(0, 200) + '…' : args.value))
</script>
