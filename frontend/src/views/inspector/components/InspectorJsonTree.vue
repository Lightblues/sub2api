<template>
  <div class="font-mono text-xs leading-relaxed">
    <JsonNode :value="value" :name="rootName" :depth="0" :defaultDepth="defaultDepth" />
  </div>
</template>

<script setup lang="ts">
import JsonNode from './InspectorJsonNode.vue'

defineProps<{
  value: unknown
  rootName?: string
  defaultDepth?: number
}>()
</script>
