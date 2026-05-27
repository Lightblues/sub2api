<template>
  <span
    class="ml-1.5 inline-block cursor-pointer rounded px-1 text-[11px] text-gray-500 opacity-0 transition-opacity hover:bg-gray-200 hover:text-blue-500 group-hover/node:opacity-100 dark:hover:bg-gray-700 dark:hover:text-blue-400"
    title="Copy JSON"
    @click.stop="copy"
  >
    {{ hit ? '✓' : '⎘' }}
  </span>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const props = defineProps<{ value: unknown }>()
const hit = ref(false)

async function copy() {
  const text = typeof props.value === 'string' ? props.value : JSON.stringify(props.value, null, 2)
  try {
    await navigator.clipboard.writeText(text)
    hit.value = true
  } catch {
    hit.value = false
  }
  setTimeout(() => (hit.value = false), 800)
}
</script>
