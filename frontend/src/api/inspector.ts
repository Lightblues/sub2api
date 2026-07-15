import { apiClient } from './client'

export interface InspectorDateEntry {
  date: string
  records: number
  archived: boolean
}

export interface InspectorDateStats {
  date: string
  total_requests: number
  by_model: Record<string, number>
  by_api_key_id: Record<string, number>
  sessions: InspectorSessionSummary[]
  tokens: {
    total_input: number
    total_output: number
    total_cached: number
    total_reasoning: number
    total: number
  }
}

export interface InspectorSessionSummary {
  type: string
  id: string
  count: number
}

export interface InspectorLogEntry {
  id: number
  ts: number
  time: string
  api_key_id: number
  model: string
  session: { type: string; id: string } | null
  status: string
  input_tokens: number
  output_tokens: number
  cached_tokens: number
  reasoning_tokens: number
  total_tokens: number
}

export interface InspectorLogResult {
  date: string
  total_matched: number
  offset: number
  limit: number
  data: InspectorLogEntry[]
}

export interface InspectorRecordDetail {
  record: Record<string, unknown>
  summary: {
    id: number
    ts: number
    time: string
    api_key_id: number
    model: string
    session: { type: string; id: string } | null
    user_agent: string
    status: string
    input_tokens: number
    output_tokens: number
    cached_tokens: number
    reasoning_tokens: number
    total_tokens: number
  }
}

export interface InspectorKeyInfo {
  id: number
  name: string
  key_prefix: string
}

export interface InspectorLogFilter {
  session_id?: string
  api_key_id?: number
  model?: string
  q?: string
  offset?: number
  limit?: number
  /** "asc" | "desc" — undefined defaults to "desc" (newest first) on the server side. */
  order?: 'asc' | 'desc'
}

async function getDates(): Promise<InspectorDateEntry[]> {
  const { data } = await apiClient.get<{ dates: InspectorDateEntry[] }>('/inspector/dates')
  return (data as unknown as { dates: InspectorDateEntry[] }).dates
}

async function getDateStats(date: string): Promise<InspectorDateStats> {
  const { data } = await apiClient.get<InspectorDateStats>(`/inspector/dates/${date}/stats`)
  return data as unknown as InspectorDateStats
}

async function getLogs(date: string, filter: InspectorLogFilter = {}): Promise<InspectorLogResult> {
  const { data } = await apiClient.get<InspectorLogResult>(`/inspector/dates/${date}/logs`, {
    params: filter
  })
  return data as unknown as InspectorLogResult
}

async function getRecord(id: number): Promise<InspectorRecordDetail> {
  const { data } = await apiClient.get<InspectorRecordDetail>(`/inspector/records/${id}`)
  return data as unknown as InspectorRecordDetail
}

async function getKeys(): Promise<InspectorKeyInfo[]> {
  const { data } = await apiClient.get<{ keys: InspectorKeyInfo[] }>('/inspector/keys')
  return (data as unknown as { keys: InspectorKeyInfo[] }).keys ?? []
}

function getExportUrl(date: string, filter: InspectorLogFilter = {}): string {
  const params = new URLSearchParams()
  if (filter.session_id) params.set('session_id', filter.session_id)
  if (filter.api_key_id) params.set('api_key_id', String(filter.api_key_id))
  if (filter.model) params.set('model', filter.model)
  if (filter.q) params.set('q', filter.q)
  const qs = params.toString()
  return `/api/v1/inspector/dates/${date}/export${qs ? '?' + qs : ''}`
}

export const inspectorAPI = {
  getDates,
  getDateStats,
  getLogs,
  getRecord,
  getKeys,
  getExportUrl
}
