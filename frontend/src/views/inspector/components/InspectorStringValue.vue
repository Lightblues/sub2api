<template>
  <!-- Embedded JSON string: auto-parse and render as nested tree -->
  <span v-if="parsedJson !== undefined">
    <span class="text-gray-500 dark:text-gray-400">(json string)</span>
    <div class="mt-0.5 pl-2">
      <InspectorJsonTree :value="parsedJson" :defaultDepth="Math.max(1, defaultDepth - 1)" />
    </div>
  </span>

  <!-- Long string: expandable -->
  <span v-else-if="s.length > 500">
    <span class="text-blue-300 dark:text-blue-200">
      {{ expanded ? JSON.stringify(s) : JSON.stringify(s.slice(0, 200) + '…') }}
    </span>
    <button
      class="ml-2 text-blue-500 hover:text-blue-700 dark:text-blue-400"
      @click.stop="expanded = !expanded"
    >
      [{{ expanded ? 'collapse' : `expand ${s.length} chars` }}]
    </button>
  </span>

  <!-- Short string -->
  <span v-else class="text-blue-300 dark:text-blue-200">{{ JSON.stringify(s) }}</span>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import InspectorJsonTree from './InspectorJsonTree.vue'

const props = defineProps<{
  s: string
  defaultDepth: number
}>()

const expanded = ref(false)

const parsedJson = computed(() => {
  const trimmed = props.s.trim()
  if (
    (trimmed.startsWith('{') && trimmed.endsWith('}')) ||
    (trimmed.startsWith('[') && trimmed.endsWith(']'))
  ) {
    try {
      const parsed = JSON.parse(trimmed)
      if (typeof parsed === 'object' && parsed !== null) return parsed
    } catch {
      /* not JSON */
    }
  }
  return undefined
})
</script>
