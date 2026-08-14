// REST 客户端：token 注入、错误规整、401 跳登录。
const TOKEN_KEY = 'tp_token'
const PROJECT_KEY = 'tp_project'

export function getToken() {
  return localStorage.getItem(TOKEN_KEY)
}
export function setToken(t: string | null) {
  if (t) localStorage.setItem(TOKEN_KEY, t)
  else localStorage.removeItem(TOKEN_KEY)
}
export function getProjectId(): string {
  return localStorage.getItem(PROJECT_KEY) || ''
}
export function setProjectId(id: string) {
  localStorage.setItem(PROJECT_KEY, id)
}
const ENV_KEY = 'tp_env'
export function getEnvId(): string {
  return localStorage.getItem(ENV_KEY) || ''
}
export function setEnvId(id: string) {
  localStorage.setItem(ENV_KEY, id)
}

// download 带 Bearer 的 blob 下载（导出端点需认证，<a href> 会 401）。
export async function download(path: string, filename: string) {
  const res = await fetch(path, {
    headers: getToken() ? { Authorization: `Bearer ${getToken()}` } : {},
  })
  if (!res.ok) throw new Error(`下载失败 HTTP ${res.status}`)
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export async function api<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(getToken() ? { Authorization: `Bearer ${getToken()}` } : {}),
      ...(opts.headers || {}),
    },
  })
  if (res.status === 401) {
    setToken(null)
    if (!location.hash.includes('/login')) location.hash = '#/login'
    throw new Error('登录已过期')
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = (data as any).error
    // 错误码体系：{error:{code,message}}；兼容旧字符串形态
    const text = err?.message ? `[${err.code}] ${err.message}` : typeof err === 'string' ? err : `HTTP ${res.status}`
    throw new Error(text)
  }
  return data as T
}

export const get = <T = any>(path: string) => api<T>(path)
export const post = <T = any>(path: string, body?: any) =>
  api<T>(path, { method: 'POST', body: JSON.stringify(body ?? {}) })
export const put = <T = any>(path: string, body?: any) =>
  api<T>(path, { method: 'PUT', body: JSON.stringify(body ?? {}) })
export const del = <T = any>(path: string) => api<T>(path, { method: 'DELETE' })

export interface ListResp<T> {
  items: T[]
  total: number
}

export interface Project {
  id: string
  name: string
  description: string
}
export interface Environment {
  id: string
  project_id: string
  name: string
  base_url: string
  icon: string
}
export interface Variable {
  id: string
  project_id: string
  environment_id: string
  key: string
  value: string
  sensitive: boolean
  description: string
}
export interface HttpApi {
  id: string
  method: number
  uri: string
  params?: { key: string; value: string }[]
  headers?: { key: string; value: string }[]
  body?: { contentType: number; raw?: string }
}
export interface GrpcApi {
  id: string
  project_id: string
  proto_ref?: string
  address: string
  full_service: string
  method: string
  request_message?: any
  metadata?: { key: string; value: string }[]
  deadline_ms?: number
  tls_settings?: any
}
export interface ProtoFile {
  id: string
  project_id: string
  filename: string
  content: string
  imports?: string[]
}
export interface Suite {
  id: string
  project_id: string
  name: string
  description: string
  case_ids?: string[]
}
export interface Script {
  id: string
  project_id: string
  name: string
  description: string
  language: string
  content: string
}
export interface TenantView {
  tenant_id: string
  name: string
  role: number
  is_current: boolean
}
export interface Me {
  user: { id: string; username: string; display_name: string }
  tenant_id: string
  role: number
}
export interface Schedule {
  id: string
  plan_id: string
  env_id: string
  cron_expr: string
  overlap_policy: number
  enabled: boolean
}
export interface NotificationChannel {
  id: string
  type: number // 1=webhook 2=dingtalk 3=feishu
  name: string
  events: string
  webhook_url: string
  secret: string
}
export interface IdentityProvider {
  id: string
  name: string
  type: string // oidc | oauth2
  issuer: string
  client_id: string
  enabled: boolean
  config?: Record<string, string>
}
export interface TenantQuota {
  metric: string
  limit: number
  used: number
}
export interface TenantSetting {
  id: string
  key: string
  value: string
}
export interface Member {
  user_id: string
  username: string
  display_name: string
  role: number
}
export interface AuditLog {
  id: string
  actor: number
  actor_id: string
  action: string
  resource_type: string
  resource_id: string
  detail?: any
  created_at: string
}
export interface StressPlan {
  id: string
  project_id: string
  env_id: string
  target_type: number // 1=api 2=behavior_case
  target_id: string
  load_profile: any
  worker_count: number
  metrics_interval_ms: number
}
export interface DebugRequest {
  project_id: string
  api_id?: string
  method?: number
  uri?: string
  params?: { key: string; value: string }[]
  headers?: { key: string; value: string }[]
  body?: { contentType: number; raw?: string }
  env_id?: string
  timeout_ms?: number
}
export interface DebugResult {
  run_id: string
  case_result_id: string
  status: number
  duration_ms: number
  error: string
  step?: {
    step_path: string
    status: number
    duration_ms: number
    request?: any
    response?: any
    assertions?: any[]
    logs?: string[]
  }
}
export interface TestCase {
  id: string
  project_id: string
  type: number
  name: string
  description: string
  definition?: any
}
export interface PlanItem {
  id?: string
  ref_type: number
  ref_id: string
  enabled: boolean
  order: number
}
export interface TestPlan {
  id: string
  project_id: string
  env_id: string
  name: string
  concurrency: number
  timeout_ms: number
  items?: PlanItem[]
}
export interface Artifact {
  id: string
  kind: number // 1=screenshot 2=video 3=trace 4=har 5=download 6=log
  uri: string
  size: number
}
export interface StepResult {
  step_path: string
  status: number
  duration_ms: number
  request?: any
  response?: any
  assertions?: { assertion: any; passed: boolean; actual: string; message: string }[]
  logs?: string[]
  artifacts?: Artifact[]
}
export interface CaseResult {
  id: string
  case_id: string
  case_name: string
  status: number
  duration_ms: number
  error: string
  steps: StepResult[]
}
export interface TestRun {
  id: string
  plan_id: string
  env_id: string
  status: number
  trigger: number
  triggered_by: string
  summary?: { total: number; passed: number; failed: number; skipped: number; error?: string }
  started_at: string
  finished_at?: string
  cases?: CaseResult[]
}
export interface WorkerInfo {
  id: string
  name: string
  capabilities: number[]
  tenant_id: string
  load: number
  max_concurrency: number
  tags: string[]
  sdk_version: string
}

export const HTTP_METHODS: Record<number, { text: string; color: string }> = {
  1: { text: 'GET', color: 'green' },
  2: { text: 'POST', color: 'blue' },
  3: { text: 'PUT', color: 'orange' },
  4: { text: 'DELETE', color: 'red' },
  5: { text: 'PATCH', color: 'purple' },
  6: { text: 'HEAD', color: 'default' },
  7: { text: 'OPTIONS', color: 'default' },
}

export const STATUS: Record<number, { text: string; color: string }> = {
  0: { text: '未知', color: 'default' },
  1: { text: '运行中', color: 'processing' },
  2: { text: '通过', color: 'success' },
  3: { text: '失败', color: 'error' },
  4: { text: '跳过', color: 'warning' },
  5: { text: '超时', color: 'error' },
}

export const CAPS: Record<number, string> = {
  1: '功能测试',
  2: '低代码',
  3: 'Playwright',
  4: '压测',
}
