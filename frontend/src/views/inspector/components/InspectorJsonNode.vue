<template>
  <!-- Null -->
  <div v-if="value === null" class="pl-3.5">
    <span v-if="hasKey" class="text-sky-400 dark:text-sky-300">{{ JSON.stringify(name) }}</span>
    <span v-if="hasKey">: </span>
    <span class="text-red-400">null</span>
    <CopyBtn :value="null" />
  </div>

  <!-- Boolean -->
  <div v-else-if="typeof value === 'boolean'" class="pl-3.5">
    <span v-if="hasKey" class="text-sky-400 dark:text-sky-300">{{ JSON.stringify(name) }}</span>
    <span v-if="hasKey">: </span>
    <span class="text-red-400">{{ String(value) }}</span>
    <CopyBtn :value="value" />
  </div>

  <!-- Number -->
  <div v-else-if="typeof value === 'number'" class="pl-3.5">
    <span v-if="hasKey" class="text-sky-400 dark:text-sky-300">{{ JSON.stringify(name) }}</span>
    <span v-if="hasKey">: </span>
    <span class="text-orange-400">{{ String(value) }}</span>
    <CopyBtn :value="value" />
  </div>

  <!-- String -->
  <div v-else-if="typeof value === 'string'" class="pl-3.5">
    <span v-if="hasKey" class="text-sky-400 dark:text-sky-300">{{ JSON.stringify(name) }}</span>
    <span v-if="hasKey">: </span>
    <StringValue :s="value" :defaultDepth="(defaultDepth ?? 2)" />
    <CopyBtn :value="value" />
  </div>

  <!-- Array / Object -->
  <div v-else-if="typeof value === 'object'" class="group/node">
    <div class="flex items-start">
      <span
        class="inline-block w-3.5 cursor-pointer text-center text-gray-500 select-none"
        @click="open = !open"
      >{{ open ? '▾' : '▸' }}</span>
      <span v-if="hasKey" class="text-sky-400 dark:text-sky-300">{{ JSON.stringify(name) }}</span>
      <span v-if="hasKey">: </span>
      <span>{{ openBracket }}</span>
      <span v-if="!open" class="italic text-gray-500"> {{ summaryText }} </span>
      <span v-if="!open">{{ closeBracket }}</span>
      <CopyBtn :value="value" />
    </div>
    <div v-if="open" class="pl-4">
      <InspectorJsonNode
        v-for="[k, v] in entries"
        :key="String(k)"
        :value="v"
        :name="k"
        :depth="depth + 1"
        :defaultDepth="defaultDepth"
      />
      <div>{{ closeBracket }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import CopyBtn from './InspectorCopyBtn.vue'
import StringValue from './InspectorStringValue.vue'
import InspectorJsonNode from './InspectorJsonNode.vue'

const props = withDefaults(
  defineProps<{
    value: unknown
    name?: string | number
    depth?: number
    defaultDepth?: number
  }>(),
  { depth: 0, defaultDepth: 2 }
)

const open = ref(props.depth < props.defaultDepth)

const hasKey = computed(() => props.name !== undefined)
const isArr = computed(() => Array.isArray(props.value))
const openBracket = computed(() => (isArr.value ? '[' : '{'))
const closeBracket = computed(() => (isArr.value ? ']' : '}'))

const entries = computed<[string | number, unknown][]>(() => {
  if (isArr.value) return (props.value as unknown[]).map((x, i) => [i, x])
  return Object.entries(props.value as Record<string, unknown>)
})

const summaryText = computed(() => {
  if (isArr.value) return `${(props.value as unknown[]).length} items`
  return `${Object.keys(props.value as object).length} keys`
})
</script>
