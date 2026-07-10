<template>
  <div v-if="!messages || messages.length === 0" class="py-8 text-center text-gray-400 dark:text-gray-500">
    No messages.
  </div>
  <div v-else class="space-y-2">
    <MessageItem
      v-for="(m, i) in messages"
      :key="i"
      :message="m"
      :idx="i"
      :highlight="i === highlightIdx"
    />
  </div>
</template>

<script setup lang="ts">
import MessageItem from './InspectorMessageItem.vue'
import type { NormalizedMessage } from '../lib/messages'

defineProps<{
  messages: NormalizedMessage[] | null
  highlightIdx?: number | null
}>()
</script>
