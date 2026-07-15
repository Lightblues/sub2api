<template>
  <AppLayout>
    <div class="mx-auto max-w-[1800px] px-4 py-4">
      <!-- Header -->
      <div class="mb-3 flex items-center justify-between rounded-lg border border-gray-200 bg-white px-4 py-2.5 dark:border-gray-700 dark:bg-gray-800">
        <div class="flex items-center gap-3">
          <h1 class="text-lg font-bold text-gray-900 dark:text-white">{{ t('inspector.title') }}</h1>
          <select
            v-model="selectedDate"
            class="rounded-md border border-gray-300 bg-white px-3 py-1.5 text-sm focus:border-blue-500 focus:ring-2 focus:ring-blue-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
          >
            <option value="">{{ t('inspector.selectDate') }}</option>
            <option v-for="d in dates" :key="d.date" :value="d.date">
              {{ d.date }} ({{ fmtNum(d.records) }} reqs{{ d.archived ? ' · archived' : '' }})
            </option>
          </select>
          <button
            class="rounded-md bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700"
            @click="loadDate"
          >
            {{ t('inspector.load') }}
          </button>
          <button
            v-if="stats"
            class="text-xs text-gray-500 hover:text-gray-700 dark:text-gray-400"
            @click="statsOpen = !statsOpen"
          >
            {{ statsOpen ? '▾ Hide stats' : '▸ Show stats' }}
          </button>
        </div>
        <div class="text-xs text-gray-500 dark:text-gray-400">{{ statusText }}</div>
      </div>

      <!-- Stats cards (collapsible) -->
      <template v-if="stats && statsOpen">
        <div class="mb-3 grid grid-cols-2 gap-2 md:grid-cols-5">
          <StatCard :label="t('inspector.requests')" :value="fmtNum(stats.total_requests)" />
          <StatCard :label="t('inspector.inputTokens')" :value="fmtTokens(stats.tokens.total_input)" />
          <StatCard :label="t('inspector.outputTokens')" :value="fmtTokens(stats.tokens.total_output)" />
          <StatCard :label="t('inspector.cached')" :value="fmtTokens(stats.tokens.total_cached)" />
          <StatCard :label="t('inspector.sessions')" :value="String(stats.sessions?.length ?? 0)" />
        </div>

        <div class="mb-2 flex gap-4 text-xs text-gray-600 dark:text-gray-400">
          <div>
            <b>{{ t('inspector.models') }}:</b>
            {{ Object.entries(stats.by_model).map(([m, c]) => `${m}(${c})`).join(', ') }}
          </div>
          <div>
            <b>{{ t('inspector.keys') }}:</b>
            {{ Object.entries(stats.by_api_key_id).map(([k, c]) => `${keyName(Number(k))}(${c})`).join(', ') }}
          </div>
        </div>

        <!-- Session quick-filter badges -->
        <div
          v-if="stats.sessions?.length"
          class="mb-2 flex max-h-20 flex-wrap gap-1.5 overflow-y-auto rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 dark:border-gray-700 dark:bg-gray-800/50"
        >
          <button
            v-for="ses in stats.sessions"
            :key="ses.id"
            class="inline-block rounded-full px-2 py-0.5 text-[11px] font-medium hover:opacity-80"
            :class="ses.type === 'codex' ? 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300' : 'bg-pink-100 text-pink-800 dark:bg-pink-900/40 dark:text-pink-300'"
            @click="quickFilterSession(ses.id)"
          >
            {{ ses.type }}:{{ ses.id.substring(0, 8) }}… ({{ ses.count }})
          </button>
        </div>
      </template>

      <!-- Filters -->
      <div class="mb-2 flex flex-wrap items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-2 dark:border-gray-700 dark:bg-gray-800">
        <input
          v-model="filterSessionId"
          type="text"
          :placeholder="t('inspector.sessionId')"
          class="w-56 rounded border border-gray-300 px-2 py-1 font-mono text-xs dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
          @keydown.enter="applyFilters"
        />
        <select
          v-model="filterKeyId"
          class="rounded border border-gray-300 px-2 py-1 text-xs dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
        >
          <option value="">{{ t('inspector.allKeys') }}</option>
          <option v-for="[k, c] in sortedKeys" :key="k" :value="k">
            {{ keyName(Number(k)) }} ({{ c }})
          </option>
        </select>
        <select
          v-model="filterModel"
          class="rounded border border-gray-300 px-2 py-1 text-xs dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
        >
          <option value="">{{ t('inspector.allModels') }}</option>
          <option v-for="[m, c] in sortedModels" :key="m" :value="m">
            {{ m }} ({{ c }})
          </option>
        </select>
        <input
          v-model="filterQuery"
          type="text"
          :placeholder="t('inspector.fullTextSearch')"
          class="w-40 rounded border border-gray-300 px-2 py-1 text-xs dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
          @keydown.enter="applyFilters"
        />
        <!-- [custom] sort direction: default desc (newest first) -->
        <select
          v-model="sortOrder"
          class="rounded border border-gray-300 px-2 py-1 text-xs dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
          :title="t('inspector.sortOrder')"
          @change="applyFilters"
        >
          <option value="desc">{{ t('inspector.sortDesc') }}</option>
          <option value="asc">{{ t('inspector.sortAsc') }}</option>
        </select>
        <button
          class="rounded bg-gray-700 px-3 py-1 text-xs text-white hover:bg-gray-800"
          @click="applyFilters"
        >
          {{ t('inspector.filter') }}
        </button>
        <button
          class="px-2 py-1 text-xs text-gray-500 hover:text-gray-700 dark:text-gray-400"
          @click="clearFilters"
        >
          {{ t('inspector.clear') }}
        </button>
        <div class="flex-1" />
        <span class="text-xs text-gray-500 dark:text-gray-400">{{ totalMatched }} {{ t('inspector.matched') }}</span>
        <a
          :href="exportUrl"
          class="rounded bg-green-600 px-3 py-1 text-xs text-white hover:bg-green-700"
          target="_blank"
        >
          {{ t('inspector.exportJsonl') }}
        </a>
      </div>

      <!-- Table -->
      <div class="overflow-auto rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800" :style="{ maxHeight: tableMaxHeight }">
        <table class="w-full text-xs">
          <thead class="sticky top-0 z-10 bg-gray-100 dark:bg-gray-700">
            <tr class="text-left text-gray-500 dark:text-gray-400">
              <th class="w-8 px-2 py-2">#</th>
              <th class="w-[70px] px-2 py-2">{{ t('inspector.time') }}</th>
              <th class="w-16 px-2 py-2">{{ t('inspector.key') }}</th>
              <th class="px-2 py-2">{{ t('inspector.model') }}</th>
              <th class="px-2 py-2">{{ t('inspector.session') }}</th>
              <th class="w-16 px-2 py-2 text-right">In</th>
              <th class="w-16 px-2 py-2 text-right">Out</th>
              <th class="w-16 px-2 py-2 text-right">Cache</th>
              <th class="w-14 px-2 py-2">{{ t('inspector.status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="(r, i) in logEntries"
              :key="r.id"
              class="cursor-pointer border-b border-gray-100 transition-colors hover:bg-blue-50 dark:border-gray-700 dark:hover:bg-blue-900/20"
              :class="[
                i % 2 === 0 ? 'bg-white dark:bg-gray-800' : 'bg-gray-50/50 dark:bg-gray-800/50',
                selectedRecordId === r.id ? '!bg-blue-100 dark:!bg-blue-900/30' : ''
              ]"
              @click="loadDetail(r.id)"
            >
              <td class="px-2 py-1.5 text-gray-400">{{ offset + i }}</td>
              <td class="px-2 py-1.5 font-mono">{{ r.time || '-' }}</td>
              <td class="px-2 py-1.5" :title="`Key ID: ${r.api_key_id}`">{{ keyName(r.api_key_id) }}</td>
              <td class="px-2 py-1.5">
                <span class="inline-block rounded-full bg-gray-100 px-1.5 py-0.5 text-[11px] font-medium dark:bg-gray-700">
                  {{ r.model || '-' }}
                </span>
              </td>
              <td class="px-2 py-1.5">
                <span
                  v-if="r.session"
                  class="inline-block rounded-full px-1.5 py-0.5 text-[11px] font-medium"
                  :class="r.session.type === 'codex' ? 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300' : 'bg-pink-100 text-pink-800 dark:bg-pink-900/40 dark:text-pink-300'"
                >
                  {{ r.session.type }}:{{ r.session.id.substring(0, 8) }}
                </span>
                <span v-else class="text-gray-300 dark:text-gray-600">-</span>
              </td>
              <td class="px-2 py-1.5 text-right font-mono">{{ fmtNum(r.input_tokens) }}</td>
              <td class="px-2 py-1.5 text-right font-mono">{{ fmtNum(r.output_tokens) }}</td>
              <td class="px-2 py-1.5 text-right font-mono text-blue-600 dark:text-blue-400">{{ fmtNum(r.cached_tokens) }}</td>
              <td class="px-2 py-1.5" :class="r.status === 'completed' || r.status === 'end_turn' ? 'text-green-600 dark:text-green-400' : 'text-red-500'">
                {{ r.status || '-' }}
              </td>
            </tr>
          </tbody>
        </table>

        <div v-if="logEntries.length === 0" class="py-12 text-center text-gray-400">
          {{ currentDate ? t('inspector.noRecords') : t('inspector.selectAndLoad') }}
        </div>

        <!-- Pagination -->
        <div
          v-if="totalMatched > limit"
          class="sticky bottom-0 flex items-center justify-between border-t border-gray-200 bg-white px-4 py-2 dark:border-gray-700 dark:bg-gray-800"
        >
          <button
            :disabled="offset === 0"
            class="rounded border px-3 py-1 text-xs hover:bg-gray-50 disabled:opacity-30 dark:border-gray-600 dark:hover:bg-gray-700"
            @click="prevPage"
          >
            {{ t('inspector.previous') }}
          </button>
          <span class="text-xs text-gray-500 dark:text-gray-400">
            {{ offset + 1 }}-{{ Math.min(offset + limit, totalMatched) }} of {{ totalMatched }}
          </span>
          <button
            :disabled="offset + limit >= totalMatched"
            class="rounded border px-3 py-1 text-xs hover:bg-gray-50 disabled:opacity-30 dark:border-gray-600 dark:hover:bg-gray-700"
            @click="nextPage"
          >
            {{ t('inspector.next') }}
          </button>
        </div>
      </div>
    </div>

    <!-- Detail overlay (full-screen) -->
    <Teleport to="body">
      <div
        v-if="currentRecord"
        class="fixed inset-0 z-50 flex flex-col bg-white dark:bg-gray-900"
      >
        <!-- Detail header bar -->
        <div class="flex shrink-0 items-center justify-between border-b border-gray-200 bg-gray-50 px-6 py-2.5 dark:border-gray-700 dark:bg-gray-800">
          <div class="flex items-center gap-4">
            <span class="text-sm font-semibold text-gray-700 dark:text-gray-300">
              {{ t('inspector.requestDetail') }} #{{ currentRecord.summary.id }}
            </span>
            <span class="text-xs text-gray-500 dark:text-gray-400">
              {{ currentRecord.summary.model }} · {{ currentRecord.summary.time }}
              · In:{{ fmtNum(currentRecord.summary.input_tokens) }}
              Out:{{ fmtNum(currentRecord.summary.output_tokens) }}
            </span>
          </div>
          <div class="flex items-center gap-3">
            <!-- Tabs -->
            <div class="flex gap-0.5">
              <button
                v-for="tab in detailTabs"
                :key="tab"
                class="rounded px-3 py-1 text-xs font-medium"
                :class="currentTab === tab ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300' : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-700'"
                @click="currentTab = tab"
              >
                {{ tabLabel(tab) }}
              </button>
            </div>
            <button
              class="rounded bg-blue-50 px-2 py-1 text-xs text-blue-700 hover:bg-blue-100 dark:bg-blue-900/30 dark:text-blue-300 dark:hover:bg-blue-900/50"
              @click="copyDetail"
            >
              {{ t('inspector.copyJson') }}
            </button>
            <button
              class="rounded px-2 py-1 text-sm text-gray-500 hover:bg-gray-200 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-700"
              @click="closeDetail"
            >
              ✕ {{ t('inspector.close') }}
            </button>
          </div>
        </div>

        <!-- Detail content -->
        <div class="flex-1 overflow-auto px-6 py-4">
          <div class="mx-auto max-w-[960px]">
            <InspectorChatView
              v-if="currentTab === 'chat'"
              :messages="chatMessages"
              :highlightIdx="null"
            />
            <InspectorJsonTree
              v-else-if="currentTab === 'json'"
              :value="currentRecord.record"
              rootName="record"
              :defaultDepth="3"
            />
            <pre
              v-else-if="currentTab === 'raw'"
              class="whitespace-pre-wrap font-mono text-xs leading-relaxed text-gray-800 break-all dark:text-gray-200"
            >{{ prettyJson(currentRecord.record) }}</pre>
            <InspectorJsonTree
              v-else-if="currentTab === 'headers'"
              :value="currentRecord.record?.request_headers ?? {}"
              rootName="headers"
              :defaultDepth="2"
            />
          </div>
        </div>
      </div>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import InspectorChatView from './components/InspectorChatView.vue'
import InspectorJsonTree from './components/InspectorJsonTree.vue'
import StatCard from './components/InspectorStatCard.vue'
import {
  inspectorAPI,
  type InspectorDateEntry,
  type InspectorDateStats,
  type InspectorLogEntry,
  type InspectorRecordDetail,
  type InspectorKeyInfo
} from '@/api/inspector'
import { normalizeMessages, extractResponseOutput, type NormalizedMessage } from './lib/messages'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

type DetailTab = 'chat' | 'json' | 'raw' | 'headers'
const detailTabs: DetailTab[] = ['chat', 'json', 'raw', 'headers']

const dates = ref<InspectorDateEntry[]>([])
const selectedDate = ref('')
const currentDate = ref('')
const stats = ref<InspectorDateStats | null>(null)
const statsOpen = ref(true)
const logEntries = ref<InspectorLogEntry[]>([])
const totalMatched = ref(0)
const offset = ref(0)
const limit = 100
const statusText = ref('Ready')
const keyMap = ref<Record<number, InspectorKeyInfo>>({})

const filterSessionId = ref('')
const filterKeyId = ref('')
const filterModel = ref('')
const filterQuery = ref('')
// [custom] sort direction — default "desc" (newest first, most useful for inspection)
const sortOrder = ref<'asc' | 'desc'>('desc')

const currentRecord = ref<InspectorRecordDetail | null>(null)
const selectedRecordId = ref<number | null>(null)
const currentTab = ref<DetailTab>('chat')
const chatMessages = ref<NormalizedMessage[] | null>(null)

let skipUrlWatch = false

const tableMaxHeight = computed(() => {
  const base = statsOpen.value && stats.value ? 'calc(100vh - 380px)' : 'calc(100vh - 220px)'
  return base
})

const sortedModels = computed(() => {
  if (!stats.value) return []
  return Object.entries(stats.value.by_model).sort((a, b) => b[1] - a[1])
})

const sortedKeys = computed(() => {
  if (!stats.value) return []
  return Object.entries(stats.value.by_api_key_id).sort((a, b) => b[1] - a[1])
})

const exportUrl = computed(() => {
  if (!currentDate.value) return '#'
  return inspectorAPI.getExportUrl(currentDate.value, {
    session_id: filterSessionId.value || undefined,
    api_key_id: filterKeyId.value ? Number(filterKeyId.value) : undefined,
    model: filterModel.value || undefined,
    q: filterQuery.value || undefined,
    order: sortOrder.value
  })
})

function fmtNum(n: number | null | undefined): string {
  if (n == null) return '-'
  return n.toLocaleString()
}

function fmtTokens(n: number | null | undefined): string {
  if (n == null) return '-'
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return n.toString()
}

function keyName(id: number): string {
  const k = keyMap.value[id]
  return k ? `${id}:${k.name}` : String(id || '-')
}

function prettyJson(data: unknown): string {
  if (!data) return ''
  return JSON.stringify(data, null, 2)
}

function tabLabel(tab: DetailTab): string {
  switch (tab) {
    case 'chat': return 'Chat'
    case 'json': return 'JSON Tree'
    case 'raw': return 'Raw JSON'
    case 'headers': return 'Headers'
  }
}

// ---------------------------------------------------------------------------
// URL sync: push current state to URL query params
// ---------------------------------------------------------------------------
function syncToUrl() {
  const q: Record<string, string> = {}
  if (currentDate.value) q.date = currentDate.value
  if (filterSessionId.value) q.session_id = filterSessionId.value
  if (filterKeyId.value) q.api_key_id = filterKeyId.value
  if (filterModel.value) q.model = filterModel.value
  if (filterQuery.value) q.q = filterQuery.value
  // [custom] only persist order when non-default (default is desc)
  if (sortOrder.value === 'asc') q.order = 'asc'
  if (offset.value > 0) q.offset = String(offset.value)
  if (selectedRecordId.value) q.record = String(selectedRecordId.value)

  skipUrlWatch = true
  router.replace({ path: '/inspector', query: q }).finally(() => {
    skipUrlWatch = false
  })
}

function restoreFromUrl() {
  const q = route.query
  if (q.date && typeof q.date === 'string') selectedDate.value = q.date
  if (q.session_id && typeof q.session_id === 'string') filterSessionId.value = q.session_id
  if (q.api_key_id && typeof q.api_key_id === 'string') filterKeyId.value = q.api_key_id
  if (q.model && typeof q.model === 'string') filterModel.value = q.model
  if (q.q && typeof q.q === 'string') filterQuery.value = q.q
  // [custom] restore sort order (default desc)
  if (q.order === 'asc' || q.order === 'desc') sortOrder.value = q.order
  if (q.offset) offset.value = Number(q.offset) || 0
  // ts is a legacy param from Usage deep-link
  if (q.ts && typeof q.ts === 'string' && !filterQuery.value) filterQuery.value = q.ts
}

// ---------------------------------------------------------------------------
// Data loading
// ---------------------------------------------------------------------------
async function loadKeyMap() {
  try {
    const keys = await inspectorAPI.getKeys()
    const map: Record<number, InspectorKeyInfo> = {}
    for (const k of keys) map[k.id] = k
    keyMap.value = map
  } catch { /* silently skip */ }
}

async function loadDates() {
  try {
    dates.value = await inspectorAPI.getDates()
    if (dates.value.length > 0 && !selectedDate.value) {
      selectedDate.value = dates.value[0].date
    }
  } catch (e: unknown) {
    statusText.value = 'Error: ' + (e as Error).message
  }
}

async function loadDate() {
  if (!selectedDate.value) return
  currentDate.value = selectedDate.value
  statusText.value = 'Loading stats...'

  try {
    stats.value = await inspectorAPI.getDateStats(currentDate.value)
    offset.value = 0
    await loadLogs()
    statusText.value = 'Ready'
    syncToUrl()
  } catch (e: unknown) {
    statusText.value = 'Error: ' + (e as Error).message
  }
}

async function loadLogs() {
  if (!currentDate.value) return
  statusText.value = 'Loading logs...'

  try {
    const result = await inspectorAPI.getLogs(currentDate.value, {
      session_id: filterSessionId.value || undefined,
      api_key_id: filterKeyId.value ? Number(filterKeyId.value) : undefined,
      model: filterModel.value || undefined,
      q: filterQuery.value || undefined,
      order: sortOrder.value,
      offset: offset.value,
      limit
    })
    logEntries.value = result.data ?? []
    totalMatched.value = result.total_matched
    statusText.value = 'Ready'
  } catch (e: unknown) {
    statusText.value = 'Error: ' + (e as Error).message
  }
}

async function loadDetail(recordId: number) {
  selectedRecordId.value = recordId
  statusText.value = 'Loading detail...'

  try {
    const detail = await inspectorAPI.getRecord(recordId)
    currentRecord.value = detail

    const rec = detail.record as Record<string, unknown>
    const body = rec?.request_body as Record<string, unknown> | undefined
    const msgs = body ? normalizeMessages(body) : null

    // Append response output if present
    const responseOutput = extractResponseOutput(rec)
    if (msgs && responseOutput.length > 0) {
      chatMessages.value = [...msgs, ...responseOutput]
    } else {
      chatMessages.value = msgs
    }

    statusText.value = 'Ready'
    syncToUrl()
  } catch (e: unknown) {
    statusText.value = 'Error: ' + (e as Error).message
  }
}

function closeDetail() {
  currentRecord.value = null
  selectedRecordId.value = null
  chatMessages.value = null
  syncToUrl()
}

async function copyDetail() {
  if (!currentRecord.value) return
  let data: unknown
  switch (currentTab.value) {
    case 'chat':
    case 'raw':
    case 'json':
      data = currentRecord.value.record
      break
    case 'headers':
      data = (currentRecord.value.record as Record<string, unknown>)?.request_headers
      break
  }
  await navigator.clipboard.writeText(JSON.stringify(data, null, 2))
}

function applyFilters() {
  offset.value = 0
  loadLogs()
  syncToUrl()
}

function clearFilters() {
  filterSessionId.value = ''
  filterKeyId.value = ''
  filterModel.value = ''
  filterQuery.value = ''
  sortOrder.value = 'desc' // [custom] also reset sort to default
  applyFilters()
}

function quickFilterSession(id: string) {
  filterSessionId.value = id
  applyFilters()
}

function prevPage() {
  offset.value = Math.max(0, offset.value - limit)
  loadLogs()
  syncToUrl()
}

function nextPage() {
  offset.value += limit
  loadLogs()
  syncToUrl()
}

// Handle back/forward navigation
watch(
  () => route.query,
  () => {
    if (skipUrlWatch) return
    restoreFromUrl()
    if (selectedDate.value && selectedDate.value !== currentDate.value) {
      loadDate()
    }
    // Re-open record if URL has record param
    const rid = route.query.record
    if (rid && Number(rid) !== selectedRecordId.value) {
      loadDetail(Number(rid))
    }
  }
)

onMounted(async () => {
  restoreFromUrl()
  await Promise.all([loadKeyMap(), loadDates()])
  if (selectedDate.value) {
    await loadDate()
    // If URL had a record param, load it
    const rid = route.query.record
    if (rid) loadDetail(Number(rid))
  } else if (dates.value.length > 0) {
    selectedDate.value = dates.value[0].date
  }
})
</script>
