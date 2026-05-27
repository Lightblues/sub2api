<template>
  <AppLayout>
    <div class="mx-auto max-w-[1600px] px-4 py-4">
      <!-- Header -->
      <div class="mb-4 flex items-center justify-between rounded-lg border border-gray-200 bg-white px-4 py-3 dark:border-gray-700 dark:bg-gray-800">
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
        </div>
        <div class="text-xs text-gray-500 dark:text-gray-400">{{ statusText }}</div>
      </div>

      <!-- Stats cards -->
      <div
        v-if="stats"
        class="mb-4 grid grid-cols-2 gap-3 md:grid-cols-5"
      >
        <StatCard :label="t('inspector.requests')" :value="fmtNum(stats.total_requests)" />
        <StatCard :label="t('inspector.inputTokens')" :value="fmtTokens(stats.tokens.total_input)" />
        <StatCard :label="t('inspector.outputTokens')" :value="fmtTokens(stats.tokens.total_output)" />
        <StatCard :label="t('inspector.cached')" :value="fmtTokens(stats.tokens.total_cached)" />
        <StatCard :label="t('inspector.sessions')" :value="String(stats.sessions?.length ?? 0)" />
      </div>

      <!-- Stats detail -->
      <div v-if="stats" class="mb-3 flex gap-4 text-xs text-gray-600 dark:text-gray-400">
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
        v-if="stats && stats.sessions?.length"
        class="mb-3 flex max-h-24 flex-wrap gap-1.5 overflow-y-auto rounded-lg border border-gray-200 bg-gray-50 px-3 py-1.5 dark:border-gray-700 dark:bg-gray-800/50"
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

      <!-- Filters -->
      <div class="mb-3 flex flex-wrap items-center gap-2 rounded-lg border border-gray-200 bg-white px-4 py-2 dark:border-gray-700 dark:bg-gray-800">
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

      <!-- Main content: table + detail panel -->
      <div class="flex gap-0" :class="{ 'h-[calc(100vh-380px)]': true }">
        <!-- Table panel -->
        <div class="min-w-0 flex-1 overflow-auto rounded-lg border border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800">
          <table class="w-full text-xs">
            <thead class="sticky top-0 bg-gray-100 dark:bg-gray-700">
              <tr class="text-left text-gray-500 dark:text-gray-400">
                <th class="w-8 px-3 py-2">#</th>
                <th class="w-20 px-3 py-2">{{ t('inspector.time') }}</th>
                <th class="w-16 px-3 py-2">{{ t('inspector.key') }}</th>
                <th class="px-3 py-2">{{ t('inspector.model') }}</th>
                <th class="px-3 py-2">{{ t('inspector.session') }}</th>
                <th class="w-20 px-3 py-2 text-right">In</th>
                <th class="w-20 px-3 py-2 text-right">Out</th>
                <th class="w-20 px-3 py-2 text-right">Cache</th>
                <th class="w-16 px-3 py-2">{{ t('inspector.status') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="(r, i) in logEntries"
                :key="r.id"
                class="cursor-pointer border-b border-gray-100 transition-colors hover:bg-blue-50 dark:border-gray-700 dark:hover:bg-blue-900/20"
                :class="[
                  i % 2 === 0 ? 'bg-white dark:bg-gray-800' : 'bg-gray-50/50 dark:bg-gray-800/50',
                  selectedRecordId === r.id ? 'bg-blue-100 dark:bg-blue-900/30' : ''
                ]"
                @click="loadDetail(r.id)"
              >
                <td class="px-3 py-1.5 text-gray-400">{{ offset + i }}</td>
                <td class="px-3 py-1.5 font-mono">{{ r.time || '-' }}</td>
                <td class="px-3 py-1.5" :title="`Key ID: ${r.api_key_id}`">{{ keyName(r.api_key_id) }}</td>
                <td class="px-3 py-1.5">
                  <span class="inline-block rounded-full bg-gray-100 px-2 py-0.5 text-[11px] font-medium dark:bg-gray-700">
                    {{ r.model || '-' }}
                  </span>
                </td>
                <td class="px-3 py-1.5">
                  <span
                    v-if="r.session"
                    class="inline-block rounded-full px-2 py-0.5 text-[11px] font-medium"
                    :class="r.session.type === 'codex' ? 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-300' : 'bg-pink-100 text-pink-800 dark:bg-pink-900/40 dark:text-pink-300'"
                  >
                    {{ r.session.type }}:{{ r.session.id.substring(0, 8) }}
                  </span>
                  <span v-else class="text-gray-300 dark:text-gray-600">-</span>
                </td>
                <td class="px-3 py-1.5 text-right font-mono">{{ fmtNum(r.input_tokens) }}</td>
                <td class="px-3 py-1.5 text-right font-mono">{{ fmtNum(r.output_tokens) }}</td>
                <td class="px-3 py-1.5 text-right font-mono text-blue-600 dark:text-blue-400">{{ fmtNum(r.cached_tokens) }}</td>
                <td class="px-3 py-1.5" :class="r.status === 'completed' || r.status === 'end_turn' ? 'text-green-600 dark:text-green-400' : 'text-red-500'">
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
            class="flex items-center justify-between border-t border-gray-200 bg-white px-4 py-2 dark:border-gray-700 dark:bg-gray-800"
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

        <!-- Detail panel -->
        <div
          v-if="currentRecord"
          class="ml-0 w-[50%] shrink-0 overflow-hidden rounded-lg border border-l-0 border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800"
        >
          <!-- Detail header -->
          <div class="flex items-center justify-between border-b border-gray-200 bg-gray-50 px-4 py-2 dark:border-gray-700 dark:bg-gray-700/50">
            <span class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{ t('inspector.requestDetail') }}</span>
            <div class="flex gap-2">
              <button
                class="rounded bg-blue-50 px-2 py-1 text-xs text-blue-700 hover:bg-blue-100 dark:bg-blue-900/30 dark:text-blue-300 dark:hover:bg-blue-900/50"
                @click="copyDetail"
              >
                {{ t('inspector.copyJson') }}
              </button>
              <button
                class="px-2 py-1 text-xs text-gray-500 hover:text-gray-700 dark:text-gray-400"
                @click="closeDetail"
              >
                {{ t('inspector.close') }} &times;
              </button>
            </div>
          </div>

          <!-- Detail tabs -->
          <div class="flex gap-0.5 border-b border-gray-200 px-4 py-1.5 dark:border-gray-700">
            <button
              v-for="tab in detailTabs"
              :key="tab"
              class="rounded px-2 py-1 text-xs"
              :class="currentTab === tab ? 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300' : 'text-gray-500 dark:text-gray-400'"
              @click="currentTab = tab"
            >
              {{ tabLabel(tab) }}
            </button>
          </div>

          <!-- Detail content -->
          <div class="flex-1 overflow-auto p-4" style="max-height: calc(100% - 85px)">
            <!-- Chat tab -->
            <InspectorChatView
              v-if="currentTab === 'chat'"
              :messages="chatMessages"
              :highlightIdx="null"
            />

            <!-- JSON Tree tab -->
            <InspectorJsonTree
              v-else-if="currentTab === 'json'"
              :value="currentRecord.record"
              rootName="record"
              :defaultDepth="3"
            />

            <!-- Raw JSON tab -->
            <pre
              v-else-if="currentTab === 'raw'"
              class="max-h-full overflow-auto whitespace-pre-wrap font-mono text-xs leading-relaxed text-gray-800 break-all dark:text-gray-200"
            >{{ prettyJson(tabData) }}</pre>

            <!-- Headers tab -->
            <InspectorJsonTree
              v-else-if="currentTab === 'headers'"
              :value="currentRecord.record?.request_headers ?? {}"
              rootName="headers"
              :defaultDepth="2"
            />
          </div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
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
import { normalizeMessages, type NormalizedMessage } from './lib/messages'

const { t } = useI18n()
const route = useRoute()

type DetailTab = 'chat' | 'json' | 'raw' | 'headers'
const detailTabs: DetailTab[] = ['chat', 'json', 'raw', 'headers']

const dates = ref<InspectorDateEntry[]>([])
const selectedDate = ref('')
const currentDate = ref('')
const stats = ref<InspectorDateStats | null>(null)
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

const currentRecord = ref<InspectorRecordDetail | null>(null)
const selectedRecordId = ref<number | null>(null)
const currentTab = ref<DetailTab>('chat')
const chatMessages = ref<NormalizedMessage[] | null>(null)

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
    q: filterQuery.value || undefined
  })
})

const tabData = computed(() => {
  if (!currentRecord.value) return null
  if (currentTab.value === 'raw') return currentRecord.value.record
  return null
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

async function loadKeyMap() {
  try {
    const keys = await inspectorAPI.getKeys()
    const map: Record<number, InspectorKeyInfo> = {}
    for (const k of keys) {
      map[k.id] = k
    }
    keyMap.value = map
  } catch {
    // silently skip if PG not available
  }
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

    // Parse messages for chat view
    const body = detail.record?.request_body as Record<string, unknown> | undefined
    if (body) {
      chatMessages.value = normalizeMessages(body)
    } else {
      chatMessages.value = null
    }

    statusText.value = 'Ready'
  } catch (e: unknown) {
    statusText.value = 'Error: ' + (e as Error).message
  }
}

function closeDetail() {
  currentRecord.value = null
  selectedRecordId.value = null
  chatMessages.value = null
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
}

function clearFilters() {
  filterSessionId.value = ''
  filterKeyId.value = ''
  filterModel.value = ''
  filterQuery.value = ''
  applyFilters()
}

function quickFilterSession(id: string) {
  filterSessionId.value = id
  applyFilters()
}

function prevPage() {
  offset.value = Math.max(0, offset.value - limit)
  loadLogs()
}

function nextPage() {
  offset.value += limit
  loadLogs()
}

// Handle URL query params for deep-linking from Usage page
watch(
  () => route.query,
  (q) => {
    if (q.date && typeof q.date === 'string') {
      selectedDate.value = q.date
      if (q.ts) {
        filterQuery.value = String(q.ts)
      }
      if (q.model && typeof q.model === 'string') {
        filterModel.value = q.model
      }
      loadDate()
    }
  },
  { immediate: true }
)

onMounted(async () => {
  await Promise.all([loadKeyMap(), loadDates()])
  // If no deep-link triggered loading, auto-load first date
  if (!currentDate.value && dates.value.length > 0) {
    selectedDate.value = dates.value[0].date
  }
})
</script>
