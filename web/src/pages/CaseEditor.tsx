import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  Button,
  Card,
  Dropdown,
  Empty,
  Form,
  Input,
  InputNumber,
  Popconfirm,
  Segmented,
  Select,
  Space,
  Switch,
  Tooltip,
  Typography,
} from 'antd'
import {
  AimOutlined, ApiOutlined, ArrowLeftOutlined, BranchesOutlined, CheckSquareOutlined,
  ClockCircleOutlined, CloudServerOutlined, CodeOutlined, CopyOutlined, DeleteOutlined,
  PlayCircleOutlined, PlusOutlined, RedoOutlined, SyncOutlined, TagsOutlined,
} from '@ant-design/icons'
import { get, post, put, HTTP_METHODS } from '../api'
import type { GrpcApi, HttpApi, ListResp, TestCase, TestRun } from '../api'
import BodyEditor from '../components/BodyEditor'
import WrapperPreviewModal from '../components/WrapperPreviewModal'
import RunDetailDrawer from '../components/RunDetailDrawer'
import type { BodyValue } from '../components/BodyEditor'
import IdeLayout from '../components/IdeLayout'
import KvEditor from '../components/KvEditor'
import type { Kv } from '../components/KvEditor'
import { PALETTE, SPACING } from '../theme'
import useSaveShortcut from '../hooks/useSaveShortcut'
import { useLeaveGuard } from '../hooks/useLeaveGuard'
import { useStableRows } from '../hooks/useStableRows'
import { useLayout } from '../hooks/useLayout'
import { useEventStream } from '../hooks/useEventStream'
import { message } from '../messageBridge'

const MONO = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'

// ---------------------------------------------------------------------------
// 步骤树数据模型（protojson 字段名，与 DeclarativeCase / TestStep proto 对齐）
// ---------------------------------------------------------------------------

export interface OverrideParam {
  method?: number
  uri?: string
  params?: Kv[]
  headers?: Kv[]
}
export interface ApiCallParam {
  api_id?: string
  inline?: {
    method: number
    uri: string
    params?: Kv[]
    headers?: Kv[]
    body?: BodyValue
  }
  override?: OverrideParam
}
export interface GrpcCallParam {
  grpc_api_id?: string
  request_override?: any
  metadata_override?: Kv[]
}
export interface StepNode {
  id: string
  type: number
  name: string
  api_call?: ApiCallParam
  grpc_call?: GrpcCallParam
  assertion?: { assertions: { target: number; path?: string; op: number; expected?: string }[] }
  set_var?: { key?: string; value_expr?: string }
  if_step?: { condition_expr?: string; then_steps?: StepNode[]; else_steps?: StepNode[] }
  loop_step?: {
    iterator?: string
    count?: number
    range?: { start: number; end: number }
    parallel?: boolean
    body_steps?: StepNode[]
  }
  retry_step?: { body_step?: StepNode; max_attempts?: number; backoff?: string }
  code_block?: { lang?: string; source?: string }
  delay?: { duration?: string }
  ui_action?: { action?: number; target?: string; value?: string }
}

const STEP_META: Record<number, { label: string; icon: ReactNode; color: string }> = {
  1: { label: 'HTTP 调用', icon: <ApiOutlined />, color: '#4CAF50' },
  2: { label: 'gRPC 调用', icon: <CloudServerOutlined />, color: '#7C5CFC' },
  3: { label: '断言', icon: <CheckSquareOutlined />, color: '#52C41A' },
  4: { label: '设置变量', icon: <TagsOutlined />, color: '#4D6EEB' },
  5: { label: '条件', icon: <BranchesOutlined />, color: '#F56A2A' },
  6: { label: '循环', icon: <SyncOutlined />, color: '#13A8A8' },
  7: { label: '重试', icon: <RedoOutlined />, color: '#F54A45' },
  8: { label: '代码块', icon: <CodeOutlined />, color: '#646A73' },
  9: { label: '等待', icon: <ClockCircleOutlined />, color: '#FAAD14' },
  10: { label: 'UI 操作', icon: <AimOutlined />, color: '#EB2F96' },
}
const STEP_ORDER = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]

const ASSERT_TARGETS = [
  { value: 1, label: 'STATUS' },
  { value: 2, label: 'HEADER' },
  { value: 3, label: 'BODY' },
  { value: 4, label: 'JSONPATH' },
  { value: 5, label: 'ELAPSED' },
]
const ASSERT_OPS = [
  { value: 1, label: 'EQ' },
  { value: 2, label: 'NE' },
  { value: 3, label: 'EXISTS' },
  { value: 4, label: 'NOT_EXISTS' },
  { value: 5, label: 'CONTAINS' },
  { value: 6, label: 'MATCHES' },
  { value: 7, label: 'GT' },
  { value: 8, label: 'LT' },
  { value: 9, label: 'GE' },
  { value: 10, label: 'LE' },
  { value: 11, label: 'TYPE_IS' },
]
const UI_ACTIONS: Record<number, string> = {
  1: 'GOTO', 2: 'CLICK', 3: 'FILL', 4: 'SELECT', 5: 'CHECK', 6: 'HOVER', 7: 'PRESS',
  8: 'EXPECT_TEXT', 9: 'EXPECT_VISIBLE', 10: 'SCREENSHOT', 11: 'WAIT', 12: 'UPLOAD', 13: 'DOWNLOAD',
}

const LOWCODE_PLACEHOLDER = `from testpilot_sdk import Context


async def run(ctx):
    # 沙箱内无网络出口；HTTP 经能力桥由 Worker 执行
    resp = await ctx.http("GET", "/json")
    assert resp.status == 200
`

// 从脚本源码提取字面量接口依赖（与 Scheduler 派发期静态提取规则一致）；
// 动态拼接 ID 仍需要手动在「接口依赖」中选择。
function extractApiRefs(src: string): { http: string[]; grpc: string[] } {
  const ids = (re: RegExp) =>
    Array.from(src.matchAll(re), (m) => m[1])
      .filter((v, i, a) => !!v && a.indexOf(v) === i) as string[]
  return {
    http: [
      ...ids(/ctx\.http_api\(\s*["'](\d+)["']/g),
      ...ids(/HttpAPI\(\s*api_id\s*=\s*["'](\d+)["']/g),
    ],
    grpc: [
      ...ids(/ctx\.grpc_api\(\s*["'](\d+)["']/g),
      ...ids(/GrpcAPI\(\s*api_id\s*=\s*["'](\d+)["']/g),
    ],
  }
}

// ---------------------------------------------------------------------------
// 树操作（在 structuredClone 副本上可变操作，再整体 setState）
// ---------------------------------------------------------------------------

function newId(): string {
  return String(Date.now()) + Math.random()
}

function makeStep(type: number): StepNode {
  const base: StepNode = { id: newId(), type, name: STEP_META[type]?.label ?? '步骤' }
  switch (type) {
    case 1:
      return { ...base, api_call: { inline: { method: 1, uri: '', params: [], headers: [], body: { contentType: 0 } } } }
    case 2:
      return { ...base, grpc_call: { grpc_api_id: '', metadata_override: [] } }
    case 3:
      return { ...base, assertion: { assertions: [{ target: 1, path: '', op: 1, expected: '' }] } }
    case 4:
      return { ...base, set_var: { key: '', value_expr: '' } }
    case 5:
      return { ...base, if_step: { condition_expr: '', then_steps: [], else_steps: [] } }
    case 6:
      return { ...base, loop_step: { iterator: 'i', count: 1, parallel: false, body_steps: [] } }
    case 7:
      return { ...base, retry_step: { max_attempts: 3, backoff: '1s' } }
    case 8:
      return { ...base, code_block: { lang: 'python', source: '' } }
    case 9:
      return { ...base, delay: { duration: '1s' } }
    case 10:
      return { ...base, ui_action: { action: 1, target: '', value: '' } }
    default:
      return base
  }
}

// 容器节点的子节点数组（if 的 then/else、loop 的 body）
function childArrays(n: StepNode): StepNode[][] {
  const out: StepNode[][] = []
  if (n.if_step) out.push(n.if_step.then_steps ?? [], n.if_step.else_steps ?? [])
  if (n.loop_step) out.push(n.loop_step.body_steps ?? [])
  return out
}

function findInSteps(nodes: StepNode[], id: string): StepNode | undefined {
  for (const n of nodes) {
    if (n.id === id) return n
    if (n.retry_step?.body_step?.id === id) return n.retry_step.body_step
    for (const arr of childArrays(n)) {
      const f = findInSteps(arr, id)
      if (f) return f
    }
  }
  return undefined
}

// 定位目标节点所在的数组与下标；retry 的单节点子步骤定位到 retry 自身（作为兄弟插入）。
function locateArray(nodes: StepNode[], id: string): { arr: StepNode[]; index: number } | null {
  for (let i = 0; i < nodes.length; i++) {
    const n = nodes[i]
    if (n.id === id) return { arr: nodes, index: i }
    if (n.retry_step?.body_step?.id === id) return { arr: nodes, index: i }
    for (const arr of childArrays(n)) {
      const r = locateArray(arr, id)
      if (r) return r
    }
  }
  return null
}

// 深拷贝并为整棵子树重新分配 id
function reId(n: StepNode): StepNode {
  const c = structuredClone(n)
  const walk = (x: StepNode) => {
    x.id = newId()
    if (x.retry_step?.body_step) walk(x.retry_step.body_step)
    for (const arr of childArrays(x)) arr.forEach(walk)
  }
  walk(c)
  return c
}

function doInsertBelow(nodes: StepNode[], id: string, node: StepNode) {
  const loc = locateArray(nodes, id)
  if (loc) loc.arr.splice(loc.index + 1, 0, node)
  else nodes.push(node)
}

function doDuplicate(nodes: StepNode[], id: string): string | null {
  const loc = locateArray(nodes, id)
  if (!loc) return null
  const host = loc.arr[loc.index]
  if (!host) return null
  if (host.id === id) {
    const copy = reId(host)
    loc.arr.splice(loc.index + 1, 0, copy)
    return copy.id
  }
  if (host.retry_step?.body_step) {
    const copy = reId(host.retry_step.body_step)
    loc.arr.splice(loc.index + 1, 0, copy)
    return copy.id
  }
  return null
}

function doDelete(nodes: StepNode[], id: string) {
  const loc = locateArray(nodes, id)
  if (!loc) return
  const host = loc.arr[loc.index]
  if (!host) return
  if (host.id === id) loc.arr.splice(loc.index, 1)
  else if (host.retry_step?.body_step?.id === id) {
    host.retry_step = { ...host.retry_step, body_step: undefined }
  }
}

type ChildKey = 'then' | 'else' | 'body'

function doAddChild(nodes: StepNode[], id: string, key: ChildKey, node: StepNode) {
  const t = findInSteps(nodes, id)
  if (!t) return
  if (key === 'body') {
    if (t.loop_step) t.loop_step.body_steps = [...(t.loop_step.body_steps ?? []), node]
  } else if (t.if_step) {
    const k = key === 'then' ? 'then_steps' : 'else_steps'
    t.if_step[k] = [...(t.if_step[k] ?? []), node]
  }
}

function doUpdate(nodes: StepNode[], id: string, patch: Partial<StepNode>) {
  const t = findInSteps(nodes, id)
  if (t) Object.assign(t, patch)
}

function summaryOf(n: StepNode): string {
  switch (n.type) {
    case 1: {
      const a = n.api_call
      if (a?.inline) {
        const m = HTTP_METHODS[a.inline.method]?.text ?? 'GET'
        return `${m} ${a.inline.uri || '(未填 URI)'}`
      }
      return a?.api_id ? `引用接口 #${a.api_id}` : '未配置'
    }
    case 2: {
      const g = n.grpc_call
      return g?.grpc_api_id ? `引用 gRPC #${g.grpc_api_id}` : '未选择接口'
    }
    case 3:
      return `${n.assertion?.assertions?.length ?? 0} 条断言`
    case 4:
      return n.set_var?.key ? `${n.set_var.key} = ${n.set_var.value_expr || '…'}` : '未设置'
    case 5:
      return n.if_step?.condition_expr || '条件表达式'
    case 6: {
      const l = n.loop_step
      const bounds = l?.count !== undefined ? `× ${l.count}` : l?.range ? `${l.range.start}..${l.range.end}` : '?'
      return `${l?.iterator ?? 'i'} ${bounds}${l?.parallel ? ' · 并行' : ''}`
    }
    case 7:
      return `最多 ${n.retry_step?.max_attempts ?? '?'} 次 · ${n.retry_step?.backoff ?? ''}`
    case 8:
      return n.code_block?.lang ?? ''
    case 9:
      return n.delay?.duration ?? ''
    case 10: {
      const u = n.ui_action
      const label = UI_ACTIONS[u?.action ?? 0] ?? '?'
      return u?.target ? `${label} ${u.target}` : label
    }
    default:
      return ''
  }
}

// ---------------------------------------------------------------------------
// 树组件
// ---------------------------------------------------------------------------

interface TreeHandlers {
  selectedId?: string
  onSelect: (id: string) => void
  onInsertBelow: (targetId: string, node: StepNode) => void
  onDuplicate: (id: string) => void
  onDelete: (id: string) => void
  onAddChild: (parentId: string, key: ChildKey, node: StepNode) => void
}

function AddStepMenu({ onPick, children }: { onPick: (type: number) => void; children: ReactNode }) {
  return (
    <Dropdown
      trigger={['click']}
      menu={{
        items: STEP_ORDER.map((t) => ({
          key: String(t),
          label: (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
              <span style={{ color: STEP_META[t].color }}>{STEP_META[t].icon}</span>
              {STEP_META[t].label}
            </span>
          ),
        })),
        onClick: ({ key }) => onPick(Number(key)),
      }}
    >
      {children}
    </Dropdown>
  )
}

function StepRow({ node, depth, h }: { node: StepNode; depth: number; h: TreeHandlers }) {
  const [hover, setHover] = useState(false)
  const meta = STEP_META[node.type] ?? STEP_META[1]
  return (
    <div
      onClick={() => h.onSelect(node.id)}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{
        display: 'flex', alignItems: 'center', gap: 6,
        padding: `5px 8px 5px ${8 + depth * 16}px`,
        cursor: 'pointer', borderRadius: 6, marginBottom: 2,
        background: h.selectedId === node.id ? PALETTE.selectedRow : hover ? '#F3F4F6' : undefined,
      }}
    >
      <span style={{ color: meta.color, fontSize: 13, flexShrink: 0, display: 'inline-flex' }}>{meta.icon}</span>
      <span style={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'baseline', gap: 6, overflow: 'hidden' }}>
        <span style={{ fontWeight: 500, fontSize: 13, whiteSpace: 'nowrap' }}>{node.name}</span>
        <span
          style={{
            color: PALETTE.textTertiary, fontSize: 12,
            overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
          }}
        >
          {summaryOf(node)}
        </span>
      </span>
      {hover && (
        <Space size={0} style={{ flexShrink: 0 }}>
          <AddStepMenu onPick={(t) => h.onInsertBelow(node.id, makeStep(t))}>
            <Tooltip title="在下方插入步骤">
              <Button type="text" size="small" icon={<PlusOutlined />} />
            </Tooltip>
          </AddStepMenu>
          <Tooltip title="复制">
            <Button
              type="text" size="small" icon={<CopyOutlined />}
              onClick={(e) => { e.stopPropagation(); h.onDuplicate(node.id) }}
            />
          </Tooltip>
          <Popconfirm title="删除该步骤（含子步骤）？" onConfirm={() => h.onDelete(node.id)}>
            <Button
              type="text" size="small" danger icon={<DeleteOutlined />}
              onClick={(e) => e.stopPropagation()}
            />
          </Popconfirm>
        </Space>
      )}
    </div>
  )
}

function ChildBlock({
  label, nodes, parentId, childKey, depth, h,
}: {
  label: string
  nodes: StepNode[]
  parentId: string
  childKey: ChildKey
  depth: number
  h: TreeHandlers
}) {
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, paddingLeft: 8 + depth * 16, paddingTop: 2 }}>
        <span style={{ fontSize: 11, color: PALETTE.textTertiary }}>{label}</span>
        <span style={{ flex: 1, borderTop: `1px dashed ${PALETTE.border}` }} />
        <AddStepMenu onPick={(t) => h.onAddChild(parentId, childKey, makeStep(t))}>
          <Button type="text" size="small" icon={<PlusOutlined />} style={{ color: PALETTE.primary, fontSize: 12 }} />
        </AddStepMenu>
      </div>
      {nodes.length > 0 && <StepTree nodes={nodes} depth={depth} {...h} />}
      {nodes.length === 0 && (
        <div style={{ paddingLeft: 8 + depth * 16 + 14, fontSize: 11, color: PALETTE.textTertiary, paddingBottom: 2 }}>
          空
        </div>
      )}
    </div>
  )
}

// 递归步骤树：容器（if/loop）的子节点缩进渲染，retry 渲染其单个 body_step。
function StepTree({ nodes, depth = 0, ...h }: { nodes: StepNode[]; depth?: number } & TreeHandlers) {
  return (
    <div>
      {nodes.map((n) => (
        <div key={n.id}>
          <StepRow node={n} depth={depth} h={h} />
          {n.if_step && (
            <>
              <ChildBlock label="满足时" nodes={n.if_step.then_steps ?? []} parentId={n.id} childKey="then" depth={depth + 1} h={h} />
              <ChildBlock label="否则" nodes={n.if_step.else_steps ?? []} parentId={n.id} childKey="else" depth={depth + 1} h={h} />
            </>
          )}
          {n.loop_step && (
            <ChildBlock label="循环体" nodes={n.loop_step.body_steps ?? []} parentId={n.id} childKey="body" depth={depth + 1} h={h} />
          )}
          {n.retry_step?.body_step && (
            <StepTree nodes={[n.retry_step.body_step]} depth={depth + 1} {...h} />
          )}
        </div>
      ))}
    </div>
  )
}

// ---------------------------------------------------------------------------
// 表单布局（使用 antd 内置 Form / Form.Item，保留原有受控数据流与视觉密度）
// ---------------------------------------------------------------------------

const FORM_ITEM_STYLE: React.CSSProperties = { marginBottom: SPACING[4] }
const FORM_LABEL_STYLE: React.CSSProperties = { fontSize: 13, fontWeight: 500, color: PALETTE.text, padding: '0 0 6px' }
const FORM_EXTRA_STYLE: React.CSSProperties = { fontSize: 12, color: PALETTE.textTertiary, marginTop: 4 }

type OnChange = (patch: Partial<StepNode>) => void

// ---------------------------------------------------------------------------
// 各步骤类型的表单
// ---------------------------------------------------------------------------

function ApiCallForm({ node, apis, onChange }: { node: StepNode; apis: HttpApi[]; onChange: OnChange }) {
  const a = node.api_call ?? {}
  const mode: 'inline' | 'ref' = a.api_id || a.override ? 'ref' : 'inline'
  const setA = (next: ApiCallParam) => onChange({ api_call: next })
  const inline = a.inline ?? { method: 1, uri: '', params: [], headers: [], body: { contentType: 0 } }

  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="方式">
        <Segmented
          value={mode}
          onChange={(v) => {
            if (v === 'ref') setA({ api_id: '', override: {} })
            else setA({ inline: { method: 1, uri: '', params: [], headers: [], body: { contentType: 0 } } })
          }}
          options={[
            { label: '内联', value: 'inline' },
            { label: '引用接口', value: 'ref' },
          ]}
        />
      </Form.Item>
      {mode === 'inline' ? (
        <>
          <Form.Item style={FORM_ITEM_STYLE} label="方法与 URI">
            <Space.Compact block>
              <Select
                style={{ width: 110 }}
                value={inline.method}
                onChange={(v) => setA({ ...a, inline: { ...inline, method: v } })}
                options={Object.entries(HTTP_METHODS).map(([k, m]) => ({ value: Number(k), label: m.text }))}
              />
              <Input
                value={inline.uri}
                placeholder="/users/{id} 或完整 URL"
                onChange={(e) => setA({ ...a, inline: { ...inline, uri: e.target.value } })}
              />
            </Space.Compact>
          </Form.Item>
          <Form.Item style={FORM_ITEM_STYLE} label="Params">
            <KvEditor value={inline.params ?? []} onChange={(kv) => setA({ ...a, inline: { ...inline, params: kv } })} />
          </Form.Item>
          <Form.Item style={FORM_ITEM_STYLE} label="Headers">
            <KvEditor
              value={inline.headers ?? []}
              onChange={(kv) => setA({ ...a, inline: { ...inline, headers: kv } })}
              keyPlaceholder="Header 名" valuePlaceholder="Header 值（支持 {{var}}）"
            />
          </Form.Item>
          <Form.Item style={FORM_ITEM_STYLE} label="Body">
            <BodyEditor value={inline.body ?? { contentType: 0 }} onChange={(b) => setA({ ...a, inline: { ...inline, body: b } })} />
          </Form.Item>
        </>
      ) : (
        <>
          <Form.Item style={FORM_ITEM_STYLE} label="接口">
            <Select
              showSearch
              optionFilterProp="label"
              placeholder="选择接口"
              value={a.api_id || undefined}
              onChange={(v) => setA({ ...a, api_id: v })}
              options={apis.map((x) => ({ value: x.id, label: `${HTTP_METHODS[x.method]?.text ?? x.method} ${x.uri}` }))}
            />
          </Form.Item>
          <OverrideEditor value={a.override} onChange={(o) => setA({ ...a, override: o })} />
        </>
      )}
    </>
  )
}

function OverrideEditor({ value, onChange }: { value?: OverrideParam; onChange: (v: OverrideParam) => void }) {
  const ov = value ?? {}
  const has = (k: 'method' | 'uri' | 'params' | 'headers') => ov[k] !== undefined
  const toggle = (k: 'method' | 'uri' | 'params' | 'headers', on: boolean) => {
    if (on) {
      if (k === 'method') onChange({ ...ov, method: 1 })
      else if (k === 'uri') onChange({ ...ov, uri: '' })
      else if (k === 'params') onChange({ ...ov, params: [] })
      else onChange({ ...ov, headers: [] })
    } else {
      const next = { ...ov }
      delete next[k]
      onChange(next)
    }
  }
  const row = (k: 'method' | 'uri' | 'params' | 'headers', label: string, control: ReactNode) => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <Switch size="small" checked={has(k)} onChange={(v) => toggle(k, v)} />
        <span style={{ fontSize: 13, color: PALETTE.text }}>{label}</span>
      </div>
      {has(k) && control}
    </div>
  )
  return (
    <Form.Item style={FORM_ITEM_STYLE} label="覆盖（引用接口模式下可选）">
      <div style={{ border: `1px solid ${PALETTE.border}`, borderRadius: 6, padding: SPACING[3], display: 'flex', flexDirection: 'column', gap: SPACING[3] }}>
        {row('method', '方法', (
          <Select
            style={{ width: 140 }}
            value={ov.method ?? 1}
            onChange={(v) => onChange({ ...ov, method: v })}
            options={Object.entries(HTTP_METHODS).map(([k, m]) => ({ value: Number(k), label: m.text }))}
          />
        ))}
        {row('uri', 'URI', (
          <Input value={ov.uri ?? ''} placeholder="/overridden/path" onChange={(e) => onChange({ ...ov, uri: e.target.value })} />
        ))}
        {row('headers', 'Headers', (
          <KvEditor value={ov.headers ?? []} onChange={(kv) => onChange({ ...ov, headers: kv })} keyPlaceholder="Header 名" valuePlaceholder="Header 值" />
        ))}
        {row('params', 'Params', (
          <KvEditor value={ov.params ?? []} onChange={(kv) => onChange({ ...ov, params: kv })} />
        ))}
      </div>
    </Form.Item>
  )
}

function GrpcCallForm({ node, grpcApis, onChange }: { node: StepNode; grpcApis: GrpcApi[]; onChange: OnChange }) {
  const g = node.grpc_call ?? {}
  const setG = (next: GrpcCallParam) => onChange({ grpc_call: next })
  const [reqText, setReqText] = useState(() =>
    g.request_override !== undefined && g.request_override !== null
      ? JSON.stringify(g.request_override, null, 2)
      : '',
  )
  const [reqInvalid, setReqInvalid] = useState(false)

  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="gRPC 接口">
        <Select
          showSearch
          optionFilterProp="label"
          placeholder="选择 gRPC 接口"
          value={g.grpc_api_id || undefined}
          onChange={(v) => setG({ ...g, grpc_api_id: v })}
          options={grpcApis.map((x) => ({ value: x.id, label: `${x.full_service}/${x.method}` }))}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="request_override（JSON 对象）" extra="合法 JSON 对象才会写入 definition；文本为空则清除覆盖">
        <Input.TextArea
          rows={6}
          style={{ fontFamily: MONO, fontSize: 12 }}
          status={reqInvalid ? 'error' : undefined}
          value={reqText}
          placeholder={'{\n  "name": "neo"\n}'}
          onChange={(e) => {
            const t = e.target.value
            setReqText(t)
            if (!t.trim()) {
              setReqInvalid(false)
              setG({ ...g, request_override: undefined })
              return
            }
            try {
              const parsed = JSON.parse(t)
              if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
                setReqInvalid(false)
                setG({ ...g, request_override: parsed })
              } else {
                setReqInvalid(true)
              }
            } catch {
              setReqInvalid(true)
            }
          }}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="metadata_override">
        <KvEditor
          value={g.metadata_override ?? []}
          onChange={(kv) => setG({ ...g, metadata_override: kv })}
          keyPlaceholder="Metadata 键" valuePlaceholder="值"
        />
      </Form.Item>
    </>
  )
}

function AssertionForm({ node, onChange }: { node: StepNode; onChange: OnChange }) {
  const rows = node.assertion?.assertions ?? [{ target: 1, path: '', op: 1, expected: '' }]
  const rowEq = (a: any, b: any) =>
    a.target === b.target && a.op === b.op && (a.path ?? '') === (b.path ?? '') && (a.expected ?? '') === (b.expected ?? '')
  const { rows: stable, update } = useStableRows(rows, rowEq)
  const setRows = (rs: { target: number; path?: string; op: number; expected?: string }[]) =>
    onChange({ assertion: { assertions: update(rs) } })
  const setRow = (i: number, patch: Partial<{ target: number; path: string; op: number; expected: string }>) =>
    setRows(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  const needPath = (t: number) => t === 2 || t === 4

  return (
    <Form.Item style={FORM_ITEM_STYLE} label="断言行" extra="target：STATUS/HEADER/BODY/JSONPATH/ELAPSED；path 仅 HEADER / JSONPATH 需要">
      {stable.map((s, i) => {
        const r = s.item
        return (
        <div key={s.id} style={{ display: 'flex', gap: 8, alignItems: 'center', marginBottom: 8 }}>
          <Select
            style={{ width: 110 }}
            value={r.target}
            onChange={(v) => setRow(i, { target: v })}
            options={ASSERT_TARGETS}
          />
          {needPath(r.target) && (
            <Input
              style={{ width: 150 }}
              value={r.path ?? ''}
              placeholder={r.target === 4 ? '$.json.id' : 'Header 名'}
              onChange={(e) => setRow(i, { path: e.target.value })}
            />
          )}
          <Select
            style={{ width: 120 }}
            value={r.op}
            onChange={(v) => setRow(i, { op: v })}
            options={ASSERT_OPS}
          />
          <Input
            style={{ flex: 1 }}
            value={r.expected ?? ''}
            placeholder="期望值"
            onChange={(e) => setRow(i, { expected: e.target.value })}
          />
          <Button
            type="text" size="small" icon={<DeleteOutlined />}
            style={{ color: PALETTE.textTertiary }}
            onClick={() => setRows(rows.filter((_, idx) => idx !== i))}
          />
        </div>
        )
      })}
      <Button
        type="dashed" size="small" icon={<PlusOutlined />} block
        onClick={() => setRows([...rows, { target: 1, path: '', op: 1, expected: '' }])}
      >
        添加断言行
      </Button>
    </Form.Item>
  )
}

function SetVarForm({ node, onChange }: { node: StepNode; onChange: OnChange }) {
  const v = node.set_var ?? {}
  const setV = (next: { key?: string; value_expr?: string }) => onChange({ set_var: next })
  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="变量名">
        <Input
          value={v.key ?? ''}
          placeholder="变量名，如 uid"
          onChange={(e) => setV({ ...v, key: e.target.value })}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="取值表达式" extra="受限表达式：JSONPath + 安全求值器">
        <Input
          value={v.value_expr ?? ''}
          placeholder="response.json.id"
          onChange={(e) => setV({ ...v, value_expr: e.target.value })}
        />
      </Form.Item>
    </>
  )
}

function ChildrenArea({
  label, nodes, parentId, childKey, tree,
}: {
  label: string
  nodes: StepNode[]
  parentId: string
  childKey: ChildKey
  tree: TreeHandlers
}) {
  return (
    <Form.Item style={FORM_ITEM_STYLE} label={label}>
      <div style={{ border: `1px solid ${PALETTE.border}`, borderRadius: 6, padding: 8, background: '#FAFBFC' }}>
        {nodes.length ? (
          <StepTree nodes={nodes} {...tree} />
        ) : (
          <div style={{ textAlign: 'center', color: PALETTE.textTertiary, fontSize: 12, padding: '4px 0 8px' }}>
            暂无步骤
          </div>
        )}
        <AddStepMenu onPick={(t) => tree.onAddChild(parentId, childKey, makeStep(t))}>
          <Button type="dashed" size="small" icon={<PlusOutlined />} block>
            添加子步骤
          </Button>
        </AddStepMenu>
      </div>
    </Form.Item>
  )
}

function IfForm({ node, tree, onChange }: { node: StepNode; tree: TreeHandlers; onChange: OnChange }) {
  const f = node.if_step ?? { condition_expr: '', then_steps: [], else_steps: [] }
  const setF = (next: typeof f) => onChange({ if_step: next })
  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="条件表达式">
        <Input
          value={f.condition_expr ?? ''}
          placeholder="如 response.status == 200 或 {{token}} != ''"
          onChange={(e) => setF({ ...f, condition_expr: e.target.value })}
        />
      </Form.Item>
      <ChildrenArea label="满足时执行（then）" nodes={f.then_steps ?? []} parentId={node.id} childKey="then" tree={tree} />
      <ChildrenArea label="否则执行（else）" nodes={f.else_steps ?? []} parentId={node.id} childKey="else" tree={tree} />
    </>
  )
}

function LoopForm({ node, tree, onChange }: { node: StepNode; tree: TreeHandlers; onChange: OnChange }) {
  const l = node.loop_step ?? { iterator: 'i', count: 1, parallel: false, body_steps: [] }
  const setL = (next: typeof l) => onChange({ loop_step: next })
  const boundsMode: 'count' | 'range' = l.range !== undefined ? 'range' : 'count'
  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="迭代变量">
        <Input
          style={{ width: 160 }}
          value={l.iterator ?? 'i'}
          onChange={(e) => setL({ ...l, iterator: e.target.value })}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="边界">
        <Segmented
          value={boundsMode}
          onChange={(v) => {
            if (v === 'range') {
              const next = { ...l, range: { start: 0, end: (l.count ?? 1) - 1 } }
              delete next.count
              setL(next)
            } else {
              const next = { ...l, count: l.count ?? 1 }
              delete next.range
              setL(next)
            }
          }}
          options={[
            { label: '按次数', value: 'count' },
            { label: '按范围', value: 'range' },
          ]}
        />
      </Form.Item>
      {boundsMode === 'count' ? (
        <Form.Item style={FORM_ITEM_STYLE} label="次数">
          <InputNumber min={1} max={10000} value={l.count ?? 1} onChange={(v) => setL({ ...l, count: v ?? 1 })} />
        </Form.Item>
      ) : (
        <Form.Item style={FORM_ITEM_STYLE} label="范围（含端点）">
          <Space>
            <InputNumber
              value={l.range?.start ?? 0}
              onChange={(v) => setL({ ...l, range: { start: v ?? 0, end: l.range?.end ?? 0 } })}
            />
            <span style={{ color: PALETTE.textTertiary }}>至</span>
            <InputNumber
              value={l.range?.end ?? 0}
              onChange={(v) => setL({ ...l, range: { start: l.range?.start ?? 0, end: v ?? 0 } })}
            />
          </Space>
        </Form.Item>
      )}
      <Form.Item style={FORM_ITEM_STYLE} label="并行执行">
        <Switch checked={!!l.parallel} onChange={(v) => setL({ ...l, parallel: v })} />
      </Form.Item>
      <ChildrenArea label="循环体" nodes={l.body_steps ?? []} parentId={node.id} childKey="body" tree={tree} />
    </>
  )
}

function RetryForm({ node, tree, onChange }: { node: StepNode; tree: TreeHandlers; onChange: OnChange }) {
  const r = node.retry_step ?? { max_attempts: 3, backoff: '1s' }
  const setR = (next: typeof r) => onChange({ retry_step: next })
  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="最大尝试次数">
        <InputNumber min={1} max={100} value={r.max_attempts ?? 1} onChange={(v) => setR({ ...r, max_attempts: v ?? 1 })} />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="退避间隔" extra="proto Duration 文本，如 1s / 500ms">
        <Input
          style={{ width: 180 }}
          value={r.backoff ?? ''}
          placeholder="1s"
          onChange={(e) => setR({ ...r, backoff: e.target.value })}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="重试的子步骤（单个）">
        {r.body_step ? (
          <div style={{ border: `1px solid ${PALETTE.border}`, borderRadius: 6, padding: 8, background: '#FAFBFC' }}>
            <StepTree nodes={[r.body_step]} {...tree} />
          </div>
        ) : (
          <AddStepMenu onPick={(t) => setR({ ...r, body_step: makeStep(t) })}>
            <Button type="dashed" icon={<PlusOutlined />} block>
              添加子步骤
            </Button>
          </AddStepMenu>
        )}
      </Form.Item>
    </>
  )
}

function CodeBlockForm({ node, onChange }: { node: StepNode; onChange: OnChange }) {
  const c = node.code_block ?? { lang: 'python', source: '' }
  const setC = (next: typeof c) => onChange({ code_block: next })
  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="语言">
        <Select
          style={{ width: 160 }}
          value={c.lang ?? 'python'}
          onChange={(v) => setC({ ...c, lang: v })}
          options={[{ value: 'python', label: 'python' }]}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="代码" extra="在沙箱内执行：无网络出口，可访问已设置的变量">
        <Input.TextArea
          rows={12}
          style={{ fontFamily: MONO, fontSize: 12 }}
          value={c.source ?? ''}
          placeholder={'# Python 代码\nasync def main(ctx):\n    ...'}
          onChange={(e) => setC({ ...c, source: e.target.value })}
        />
      </Form.Item>
    </>
  )
}

function DelayForm({ node, onChange }: { node: StepNode; onChange: OnChange }) {
  const d = node.delay ?? { duration: '1s' }
  return (
    <Form.Item style={FORM_ITEM_STYLE} label="等待时长" extra="proto Duration 文本，如 2s / 500ms">
      <Input
        style={{ width: 200 }}
        value={d.duration ?? ''}
        placeholder="2s"
        onChange={(e) => onChange({ delay: { duration: e.target.value } })}
      />
    </Form.Item>
  )
}

function UiActionForm({ node, onChange }: { node: StepNode; onChange: OnChange }) {
  const u = node.ui_action ?? { action: 1, target: '', value: '' }
  const setU = (next: typeof u) => onChange({ ui_action: next })
  return (
    <>
      <Form.Item style={FORM_ITEM_STYLE} label="动作">
        <Select
          style={{ width: 200 }}
          value={u.action ?? 1}
          onChange={(v) => setU({ ...u, action: v })}
          options={Object.entries(UI_ACTIONS).map(([k, label]) => ({ value: Number(k), label }))}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="目标（locator）">
        <Input
          value={u.target ?? ''}
          placeholder="Playwright locator，如 #login-btn"
          onChange={(e) => setU({ ...u, target: e.target.value })}
        />
      </Form.Item>
      <Form.Item style={FORM_ITEM_STYLE} label="值">
        <Input
          value={u.value ?? ''}
          placeholder="如要填写的文本 / 按键"
          onChange={(e) => setU({ ...u, value: e.target.value })}
        />
      </Form.Item>
    </>
  )
}

function StepForm({
  node, tree, apis, grpcApis, onChange,
}: {
  node: StepNode
  tree: TreeHandlers
  apis: HttpApi[]
  grpcApis: GrpcApi[]
  onChange: OnChange
}) {
  const meta = STEP_META[node.type] ?? STEP_META[1]
  return (
    <div style={{ padding: SPACING[4], maxWidth: 760 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: SPACING[4] }}>
        <span style={{ color: meta.color, fontSize: 16, display: 'inline-flex' }}>{meta.icon}</span>
        <span style={{ fontSize: 15, fontWeight: 600, color: PALETTE.text }}>{meta.label}</span>
      </div>
      <Form
        component={false}
        layout="vertical"
        requiredMark={false}
        styles={{ label: FORM_LABEL_STYLE, extra: FORM_EXTRA_STYLE }}
      >
        <Form.Item style={FORM_ITEM_STYLE} label="名称">
          <Input value={node.name} placeholder="步骤名称" onChange={(e) => onChange({ name: e.target.value })} />
        </Form.Item>
        {node.type === 1 && <ApiCallForm node={node} apis={apis} onChange={onChange} />}
        {node.type === 2 && <GrpcCallForm node={node} grpcApis={grpcApis} onChange={onChange} />}
        {node.type === 3 && <AssertionForm node={node} onChange={onChange} />}
        {node.type === 4 && <SetVarForm node={node} onChange={onChange} />}
        {node.type === 5 && <IfForm node={node} tree={tree} onChange={onChange} />}
        {node.type === 6 && <LoopForm node={node} tree={tree} onChange={onChange} />}
        {node.type === 7 && <RetryForm node={node} tree={tree} onChange={onChange} />}
        {node.type === 8 && <CodeBlockForm node={node} onChange={onChange} />}
        {node.type === 9 && <DelayForm node={node} onChange={onChange} />}
        {node.type === 10 && <UiActionForm node={node} onChange={onChange} />}
      </Form>
    </div>
  )
}

// ---------------------------------------------------------------------------
// 用例编辑器页面
// ---------------------------------------------------------------------------

export default function CaseEditor({ onSaved }: { onSaved?: (id?: string) => void }) {
  const { id } = useParams()
  const isNew = !id
  const nav = useNavigate()
  const { projectId, envId } = useLayout()

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [caseType, setCaseType] = useState<number>(1)
  const [steps, setSteps] = useState<StepNode[]>([])
  const [selectedId, setSelectedId] = useState<string>()
  const [lowSource, setLowSource] = useState('')
  const [lowEntry, setLowEntry] = useState('run')
  const [lowParamsText, setLowParamsText] = useState('')
  const [httpRefs, setHttpRefs] = useState<string[]>([])
  const [grpcRefs, setGrpcRefs] = useState<string[]>([])
  const [wrapperPreview, setWrapperPreview] = useState('')
  const [wrapperLoading, setWrapperLoading] = useState(false)
  const [apis, setApis] = useState<HttpApi[]>([])
  const [grpcApis, setGrpcApis] = useState<GrpcApi[]>([])
  const [saving, setSaving] = useState(false)
  const [runningCase, setRunningCase] = useState(false)
  const [runDetail, setRunDetail] = useState<TestRun | null>(null)
  const [runDrawerOpen, setRunDrawerOpen] = useState(false)
  // 已保存快照（dirty 判定 + 离开守卫）
  const [savedSnap, setSavedSnap] = useState(() => JSON.stringify({ n: '', d: '', t: 1, s: [], src: '', e: 'run', p: '', hr: [], gr: [] }))

  // 声明式表单所需的接口引用
  useEffect(() => {
    if (!projectId) return
    get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page_size=500`)
      .then((r) => setApis(r.items))
      .catch(() => {})
    get<ListResp<GrpcApi>>(`/api/v1/grpc-apis?project_id=${projectId}&page_size=500`)
      .then((r) => setGrpcApis(r.items))
      .catch(() => {})
  }, [projectId])

  // 加载已有用例：definition 对象直接进编辑器状态
  useEffect(() => {
    if (!id) return
    get<TestCase>(`/api/v1/cases/${id}`)
      .then((c) => {
        let def: any = c.definition
        if (typeof def === 'string') {
          try {
            def = JSON.parse(def)
          } catch {
            def = {}
          }
        }
        const t = c.type === 2 ? 2 : 1
        setName(c.name)
        setDescription(c.description)
        setCaseType(t)
        let snap = { n: c.name, d: c.description, t, s: [] as StepNode[], src: '', e: 'run', p: '', hr: [], gr: [] }
        if (def && typeof def === 'object') {
          if (t === 2) {
            const src = typeof def.source === 'string' ? def.source : ''
            const entry = typeof def.entry === 'string' ? def.entry : 'run'
            const params = def.parameters && typeof def.parameters === 'object'
              ? JSON.stringify(def.parameters, null, 2)
              : ''
            const hr = Array.isArray(def.httpApiRefs) ? def.httpApiRefs.map(String) : []
            const gr = Array.isArray(def.grpcApiRefs) ? def.grpcApiRefs.map(String) : []
            setLowSource(src)
            setLowEntry(entry)
            setLowParamsText(params)
            setHttpRefs(hr)
            setGrpcRefs(gr)
            snap = { ...snap, src, e: entry, p: params, hr, gr }
          } else {
            const steps = Array.isArray(def.steps) ? def.steps : []
            setSteps(steps)
            snap = { ...snap, s: steps }
          }
        } else {
          setSteps([])
        }
        setSavedSnap(JSON.stringify(snap))
      })
      .catch((e) => message.error(e.message))
  }, [id])

  const snapshot = () =>
    JSON.stringify({ n: name, d: description, t: caseType, s: steps, src: lowSource, e: lowEntry, p: lowParamsText, hr: httpRefs, gr: grpcRefs })
  const dirty = snapshot() !== savedSnap
  const { guard, allowOnce } = useLeaveGuard(dirty)

  // persist：校验并保存当前编辑器内容，返回用例 ID（不负责导航）。
  const persist = async (): Promise<string | undefined> => {
    if (!projectId) {
      message.warning('请先在顶部选择项目')
      return
    }
    if (!name.trim()) {
      message.warning('请输入用例名称')
      return
    }
    let definition: any
    if (caseType === 2) {
      definition = {
        source: lowSource,
        entry: lowEntry.trim() || 'run',
        httpApiRefs: httpRefs,
        grpcApiRefs: grpcRefs,
      }
      if (lowParamsText.trim()) {
        try {
          const p = JSON.parse(lowParamsText)
          if (p && typeof p === 'object' && !Array.isArray(p)) {
            definition.parameters = p
          } else {
            message.error('parameters 必须是 JSON 对象')
            return
          }
        } catch (e: any) {
          message.error(`parameters 不是合法 JSON：${e.message}`)
          return
        }
      }
    } else {
      definition = { steps }
    }
    setSaving(true)
    try {
      // definition 必须是对象（后端 model.JSON RawMessage 列原样存储）
      const payload = { project_id: projectId, name: name.trim(), description, type: caseType, definition }
      const resp = isNew
        ? await post<TestCase>('/api/v1/cases', payload)
        : await put<TestCase>(`/api/v1/cases/${id}`, payload)
      setSavedSnap(snapshot())
      onSaved?.(resp.id)
      return resp.id
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  const save = async () => {
    const cid = await persist()
    if (!cid) return
    message.success('已保存')
    allowOnce() // setSavedSnap 提交晚于同步 nav，显式放行一次
    nav(`/cases/${cid}/edit`)
  }

  // 运行当前用例：未保存/有改动时先持久化，再触发单用例运行。
  const runNow = async () => {
    const needPersist = isNew || dirty
    const cid = needPersist ? await persist() : id
    if (!cid) return
    if (needPersist) {
      message.success('已保存，正在触发运行')
      if (isNew) {
        allowOnce()
        nav(`/cases/${cid}/edit`)
      }
    }
    setRunningCase(true)
    try {
      const r = await post<{ run_id: string }>(`/api/v1/cases/${cid}/run`, { env_id: envId || undefined })
      message.success(`已触发运行 ${r.run_id}`)
      const first = await get<TestRun>(`/api/v1/runs/${r.run_id}`)
      setRunDetail(first)
      setRunDrawerOpen(true)
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setRunningCase(false)
    }
  }
  useSaveShortcut(() => { void save() })

  // 运行结果抽屉打开且运行中 → SSE 实时刷新，结束事件也会推送到位
  useEventStream(
    runDetail && runDrawerOpen && (runDetail.status === 0 || runDetail.status === 1)
      ? [`run:${runDetail.id}`]
      : [],
    () => {
      const id = runDetail?.id
      if (!id) return
      void get<TestRun>(`/api/v1/runs/${id}`).then(setRunDetail).catch(() => {})
    },
  )

  const wrappersBaseUrl = () => {
    const params = [
      httpRefs.length ? `http_ids=${httpRefs.join(',')}` : '',
      grpcRefs.length ? `grpc_ids=${grpcRefs.join(',')}` : '',
    ].filter(Boolean)
    const qs = params.length ? `?${params.join('&')}` : ''
    return `/api/v1/projects/${projectId}/api-wrappers${qs}`
  }

  const previewWrappers = async () => {
    if (!projectId) return
    setWrapperLoading(true)
    try {
      const r = await get<{ source: string; count: number }>(wrappersBaseUrl())
      setWrapperPreview(r.source || '# （项目内暂无接口）')
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setWrapperLoading(false)
    }
  }

  const extractRefs = () => {
    const found = extractApiRefs(lowSource)
    if (!found.http.length && !found.grpc.length) {
      message.info('未从脚本中提取到字面量接口 ID')
      return
    }
    setHttpRefs((prev) => Array.from(new Set([...prev, ...found.http])))
    setGrpcRefs((prev) => Array.from(new Set([...prev, ...found.grpc])))
  }

  // 树操作：structuredClone 副本上可变操作后整体 setState
  const mutate = (fn: (nodes: StepNode[]) => void) => {
    setSteps((prev) => {
      const copy = structuredClone(prev)
      fn(copy)
      return copy
    })
  }
  const insertBelow = (targetId: string, node: StepNode) => {
    mutate((ns) => doInsertBelow(ns, targetId, node))
    setSelectedId(node.id)
  }
  const duplicate = (targetId: string) => {
    const copy = structuredClone(steps)
    const nid = doDuplicate(copy, targetId)
    if (nid) {
      setSteps(copy)
      setSelectedId(nid)
    }
  }
  const deleteNode = (targetId: string) => {
    mutate((ns) => doDelete(ns, targetId))
    if (selectedId === targetId) setSelectedId(undefined)
  }
  const updateNode = (patch: Partial<StepNode>) => {
    if (!selectedId) return
    mutate((ns) => doUpdate(ns, selectedId, patch))
  }
  const addChild = (parentId: string, key: ChildKey, node: StepNode) => {
    mutate((ns) => doAddChild(ns, parentId, key, node))
    setSelectedId(node.id)
  }
  const addRoot = (node: StepNode) => {
    mutate((ns) => ns.push(node))
    setSelectedId(node.id)
  }

  const selected = useMemo(() => (selectedId ? findInSteps(steps, selectedId) : undefined), [steps, selectedId])

  const tree: TreeHandlers = {
    selectedId,
    onSelect: setSelectedId,
    onInsertBelow: insertBelow,
    onDuplicate: duplicate,
    onDelete: deleteNode,
    onAddChild: addChild,
  }

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  return (
    <>
    <IdeLayout
      panelWidth={360}
      toolbar={
        <div style={{ display: 'flex', alignItems: 'center', gap: SPACING[3], flexWrap: 'wrap' }}>
          <Button size="small" icon={<ArrowLeftOutlined />} onClick={() => nav('/cases')}>
            返回
          </Button>
          <span style={{ fontSize: 13, color: PALETTE.textSecondary }}>名称</span>
          <Input
            size="small" style={{ width: 200 }} value={name} placeholder="用例名称"
            onChange={(e) => setName(e.target.value)}
          />
          {caseType === 1 && (
            <>
              <span style={{ fontSize: 13, color: PALETTE.textSecondary }}>描述</span>
              <Input
                size="small" style={{ width: 240 }} value={description} placeholder="描述（可选）"
                onChange={(e) => setDescription(e.target.value)}
              />
            </>
          )}
          <span style={{ fontSize: 13, color: PALETTE.textSecondary }}>类型</span>
          <Segmented
            size="small"
            value={caseType}
            onChange={(v) => setCaseType(v as number)}
            options={[
              { label: '声明式', value: 1 },
              { label: '低代码', value: 2 },
            ]}
          />
          {!isNew && (
            <Typography.Text
              copyable={{ text: id, tooltips: ['复制 ID', '已复制'] }}
              style={{ fontSize: 11, color: PALETTE.textTertiary, whiteSpace: 'nowrap' }}
            >
              ID {id}
            </Typography.Text>
          )}
          <span style={{ flex: 1 }} />
          <Button size="small" icon={<PlayCircleOutlined />} loading={runningCase} onClick={runNow}>
            运行
          </Button>
          <Button type="primary" size="small" loading={saving} onClick={save}>
            保存
          </Button>
        </div>
      }
      panel={
        caseType === 1 ? (
          <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
            <div style={{ padding: '10px 12px', borderBottom: `1px solid ${PALETTE.border}` }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
                <span style={{ fontSize: 15, fontWeight: 600, color: PALETTE.text }}>步骤树</span>
              </div>
              <AddStepMenu onPick={(t) => addRoot(makeStep(t))}>
                <Button type="dashed" size="small" icon={<PlusOutlined />} block>
                  添加步骤
                </Button>
              </AddStepMenu>
            </div>
            <div style={{ flex: 1, overflow: 'auto', padding: '4px 6px' }}>
              {steps.length ? (
                <StepTree nodes={steps} {...tree} />
              ) : (
                <div style={{ textAlign: 'center', color: PALETTE.textTertiary, fontSize: 12, padding: 32 }}>
                  暂无步骤，点击「添加步骤」开始
                </div>
              )}
            </div>
          </div>
        ) : undefined
      }
    >
      {caseType === 1 ? (
        <div style={{ height: '100%', overflow: 'auto', background: '#FFFFFF' }}>
          {selected ? (
            <StepForm
              key={selected.id}
              node={selected}
              tree={tree}
              apis={apis}
              grpcApis={grpcApis}
              onChange={updateNode}
            />
          ) : (
            <Empty style={{ marginTop: 96 }} description="在左侧选择一个步骤查看配置" />
          )}
        </div>
      ) : (
        <div style={{ height: '100%', overflow: 'auto', background: '#FFFFFF', padding: SPACING[4] }}>
          <div style={{ maxWidth: 860 }}>
            <Form
              component={false}
              layout="vertical"
              requiredMark={false}
              styles={{ label: FORM_LABEL_STYLE, extra: FORM_EXTRA_STYLE }}
            >
              <Form.Item style={FORM_ITEM_STYLE} label="描述"
                extra="用例用途/覆盖场景等补充说明，保存后展示在用例列表与运行报告">
                <Input
                  style={{ width: 420 }} value={description} placeholder="描述（可选）"
                  onChange={(e) => setDescription(e.target.value)}
                />
              </Form.Item>
              <Form.Item style={FORM_ITEM_STYLE} label="入口函数（可选，默认 run）"
                extra="脚本内可定义多个流程函数，通过入口函数切换；留空等价于 run">
                <Input
                  style={{ width: 220 }} value={lowEntry} placeholder="run"
                  onChange={(e) => setLowEntry(e.target.value)}
                />
              </Form.Item>
              <Form.Item style={FORM_ITEM_STYLE} label="Source（Python）" extra="脚本在沙箱中运行：无网络出口（HTTP 经能力桥由 Worker 代执行）、环境变量白名单、CPU/内存受限">
                <Input.TextArea
                  rows={18}
                  style={{ fontFamily: MONO, fontSize: 12 }}
                  value={lowSource}
                  placeholder={LOWCODE_PLACEHOLDER}
                  onChange={(e) => setLowSource(e.target.value)}
                />
              </Form.Item>
              <Form.Item style={FORM_ITEM_STYLE} label="接口依赖（按 ID 调用）"
                extra="保存为 definition.httpApiRefs/grpcApiRefs；脚本中的字面量 ID 派发时也会自动解析。动态拼接 ID 必须在此显式声明。">
                <Space wrap style={{ width: '100%' }}>
                  <Select
                    mode="multiple"
                    allowClear
                    placeholder="HTTP 接口依赖"
                    style={{ minWidth: 340 }}
                    value={httpRefs}
                    onChange={(v) => setHttpRefs(v)}
                    options={apis.map((a) => ({
                      value: String(a.id),
                      label: `[HTTP] ${a.name || `${HTTP_METHODS[a.method]?.text || 'GET'} ${a.uri}`} (${a.id})`,
                    }))}
                  />
                  <Select
                    mode="multiple"
                    allowClear
                    placeholder="gRPC 接口依赖"
                    style={{ minWidth: 340 }}
                    value={grpcRefs}
                    onChange={(v) => setGrpcRefs(v)}
                    options={grpcApis.map((a) => ({
                      value: String(a.id),
                      label: `[gRPC] ${a.full_service}/${a.method} (${a.id})`,
                    }))}
                  />
                  <Button size="small" onClick={extractRefs}>从脚本提取</Button>
                  <Button size="small" loading={wrapperLoading} onClick={previewWrappers}>
                    预览封装
                  </Button>
                </Space>
              </Form.Item>
              <Form.Item style={FORM_ITEM_STYLE} label="parameters（JSON 对象，可选）">
                <Input.TextArea
                  rows={4}
                  style={{ fontFamily: MONO, fontSize: 12 }}
                  value={lowParamsText}
                  placeholder={'{\n  "base_url": "https://api.example.com"\n}'}
                  onChange={(e) => setLowParamsText(e.target.value)}
                />
              </Form.Item>
            </Form>
          </div>
        </div>
      )}
    </IdeLayout>
    <RunDetailDrawer
      run={runDetail}
      open={runDrawerOpen}
      onClose={() => setRunDrawerOpen(false)}
    />
    <WrapperPreviewModal
      open={!!wrapperPreview}
      source={wrapperPreview}
      baseUrl={wrappersBaseUrl()}
      title="tp_api_wrappers.py（当前依赖）"
      onClose={() => setWrapperPreview('')}
    />
    {guard}
    </>
  )
}
