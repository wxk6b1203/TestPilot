// Copilot 对话页：Vercel AI SDK v7（ai + @ai-sdk/react useChat）消费 SSE + 写操作 HITL 审批。
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Input, Popconfirm, Space, Tag, Typography } from 'antd'
import {
  ArrowLeftOutlined, DeleteOutlined, EnvironmentOutlined, PlusOutlined,
  ProjectOutlined, RobotOutlined, SendOutlined, StopOutlined,
} from '@ant-design/icons'
import { useChat } from '@ai-sdk/react'
import { DefaultChatTransport, lastAssistantMessageIsCompleteWithApprovalResponses } from 'ai'
import type { UIMessage } from 'ai'
import { del, get, getToken } from '../api'
import type { ListResp } from '../api'
import { useLayout } from '../hooks/useLayout'
import { PALETTE } from '../theme'
import { message } from '../messageBridge'

interface Session {
  id: string
  title: string
  created_at: string
}

interface TrashSession {
  id: string
  title: string
  created_at: string
  deleted_at: string
  message_count: number
}

// 按 pydantic-ai VercelAIAdapter 的请求 schema（extra=forbid）重建 part：
// SDK v7 序列化会带 id/providerMetadata 等字段，后端不接受，逐字段裁剪
const sanitizePart = (p: any): any => {
  if (p.type === 'text' || p.type === 'reasoning') {
    const out: any = { type: p.type, text: p.text ?? '' }
    if (p.state) out.state = p.state
    if (p.providerMetadata) out.providerMetadata = p.providerMetadata
    return out
  }
  if (p.type === 'step-start') return { type: 'step-start' }
  if (p.type === 'dynamic-tool' || String(p.type).startsWith('tool-')) {
    const state = p.state ?? 'output-available'
    const out: any = { type: p.type, toolCallId: p.toolCallId, state }
    if (p.toolName) out.toolName = p.toolName
    // 后端各 state 的必填字段兜底（缺键会被 extra/required 校验拒绝变 422）
    if (state === 'output-available') {
      out.input = p.input ?? null
      out.output = p.output ?? null
    } else if (state === 'output-error') {
      out.input = p.input ?? null
      out.errorText = p.errorText ?? ''
    } else {
      out.input = p.input ?? null
    }
    if (p.output !== undefined && state !== 'output-available') out.output = p.output
    if (p.errorText !== undefined && state !== 'output-error') out.errorText = p.errorText
    if (p.approval) {
      out.approval = p.approval.approved === undefined
        ? { id: p.approval.id }
        : { id: p.approval.id, approved: p.approval.approved }
    }
    return out
  }
  return p
}
const sanitizeMessages = (msgs: any[]) =>
  msgs.map((m) => ({ id: m.id, role: m.role, parts: (m.parts || []).map(sanitizePart) }))

export default function Copilot() {
  // 页面左上角全局选择的项目/环境：随每次 chat 请求经 X-TP-Project-Id /
  // X-TP-Env-Id 头带给 Copilot，工具参数省略时默认作用于该上下文。
  const { projectId, envId, projects, envs } = useLayout()
  const projectIdRef = useRef(projectId)
  const envIdRef = useRef(envId)
  useEffect(() => { projectIdRef.current = projectId }, [projectId])
  useEffect(() => { envIdRef.current = envId }, [envId])
  const projectName = projects.find((p) => p.id === projectId)?.name
  const envName = envs.find((e) => e.id === envId)?.name

  const [sessionId, setSessionId] = useState('')
  const sessionIdRef = useRef('') // 供 transport 回调读取最新会话（不回环依赖 state）
  const [sessions, setSessions] = useState<Session[]>([])
  const [view, setView] = useState<'chat' | 'trash'>('chat')
  const [trash, setTrash] = useState<TrashSession[]>([])
  const [input, setInput] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)

  const loadSessions = () =>
    get<ListResp<Session>>('/api/v1/copilot/sessions?page_size=50').then((r) => setSessions(r.items))

  const loadTrash = () =>
    get<ListResp<TrashSession>>('/api/v1/copilot/trash?page_size=200')
      .then((r) => setTrash(r.items))
      .catch((e: any) => message.error(e.message))

  const openTrash = () => {
    if (busy) stop()
    setView('trash')
    loadTrash()
  }

  const backToChat = () => setView('chat')

  const confirmDeleteSession = async (s: Session) => {
    try {
      await del(`/api/v1/copilot/sessions/${s.id}`)
      message.success('已移入回收站')
      setSessions((prev) => prev.filter((x) => x.id !== s.id))
      if (sessionId === s.id) newChat()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const purgeTrash = async (id: string) => {
    try {
      await del(`/api/v1/copilot/trash/${id}`)
      message.success('已彻底删除')
      await loadTrash()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const transport = useMemo(
    () =>
      new DefaultChatTransport({
        api: '/copilot-api/chat',
        headers: () => ({
          Authorization: `Bearer ${getToken()}`,
          ...(sessionIdRef.current ? { 'X-Session-Id': sessionIdRef.current } : {}),
          ...(projectIdRef.current ? { 'X-TP-Project-Id': projectIdRef.current } : {}),
          ...(envIdRef.current ? { 'X-TP-Env-Id': envIdRef.current } : {}),
        }),
        body: () => ({ trigger: 'submit-message' }), // 后端要求 trigger=submit-message
        // 裁剪 parts 为后端 schema 允许的字段（SDK 序列化多出的 id 等会被 extra=forbid 拒绝）
        prepareSendMessagesRequest: ({ id, messages, body, headers }) => ({
          headers,
          body: { ...body, id, messages: sanitizeMessages(messages) },
        }),
        // 拦截响应头：首次响应携带 X-Session-Id，记下后续续聊沿用
        fetch: async (url, init) => {
          const res = await globalThis.fetch(url, init)
          const sid = res.headers.get('X-Session-Id')
          if (sid && sid !== sessionIdRef.current) {
            sessionIdRef.current = sid
            setSessionId(sid)
            loadSessions()
          }
          return res
        },
      }),
    [],
  )

  const {
    messages,
    sendMessage,
    setMessages,
    addToolApprovalResponse,
    stop,
    status,
  } = useChat({
    transport,
    sendAutomaticallyWhen: ({ messages: ms }) =>
      lastAssistantMessageIsCompleteWithApprovalResponses({ messages: ms }),
    onError: (e) => message.error(e instanceof Error ? e.message : String(e)),
  })

  const busy = status === 'submitted' || status === 'streaming'

  useEffect(() => {
    loadSessions()
  }, [])
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // 加载历史会话（仅展示：user/assistant 文本 + 工具摘要）
  const openSession = async (sid: string) => {
    if (busy) stop() // 流式中直接覆盖 messages 会与 SDK 写入竞争，先中止
    setView('chat')
    try {
      const r = await get<ListResp<any>>(`/api/v1/copilot/sessions/${sid}/messages`)
      const msgs: UIMessage[] = []
      for (const m of r.items) {
        if (m.role === 1) {
          msgs.push({ id: String(m.id), role: 'user', parts: [{ type: 'text', text: m.content }] })
        } else {
          const parts: any[] = []
          if (m.content) parts.push({ type: 'text', text: m.content })
          for (const tc of JSON.parse(m.tool_calls || '[]')) {
            // 后端 schema：output-available 的 input/output 均必填；
            // 无 result 的调用用 input-available（input 必填、无 output 字段）
            if (tc.result === undefined || tc.result === null) {
              parts.push({
                type: 'dynamic-tool', toolName: tc.name, toolCallId: String(m.id),
                state: 'input-available', input: tc.args ?? null,
              })
            } else {
              parts.push({
                type: 'dynamic-tool', toolName: tc.name, toolCallId: String(m.id),
                state: 'output-available', input: tc.args ?? null, output: tc.result,
              })
            }
          }
          msgs.push({ id: String(m.id), role: 'assistant', parts })
        }
      }
      sessionIdRef.current = sid
      setSessionId(sid)
      setMessages(msgs)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const newChat = () => {
    if (busy) stop()
    setView('chat')
    sessionIdRef.current = ''
    setSessionId('')
    setMessages([])
  }

  const submit = () => {
    const text = input.trim()
    if (!text || busy) return
    setInput('')
    sendMessage({ text })
  }

  // HITL：批准/拒绝 → 补审批回执，sendAutomaticallyWhen 命中后 SDK 自动续发
  const respond = (part: any, approved: boolean) => {
    if (!part.approval?.id) return
    addToolApprovalResponse({ id: part.approval.id, approved })
  }

  return (
    <div style={{ display: 'flex', height: '100%' }}>
      {/* 会话列表：常驻左栏，全高，内部滚动 */}
      <div style={{
        flex: '0 0 260px', minWidth: 0, borderRight: `1px solid ${PALETTE.border}`,
        display: 'flex', flexDirection: 'column', padding: 8, background: '#FFFFFF',
      }}>
        <Button block icon={<PlusOutlined />} onClick={newChat} style={{ marginBottom: 8, flexShrink: 0 }}>
          新会话
        </Button>
        <div style={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
          {sessions.map((s) => (
            <div
              key={s.id}
              onClick={() => openSession(s.id)}
              style={{
                cursor: 'pointer', padding: '6px 8px', borderRadius: 6, margin: '2px 4px',
                display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8,
                background: s.id === sessionId ? PALETTE.selectedRow : undefined,
              }}
            >
              <Typography.Text ellipsis style={{ fontSize: 13, flex: 1, minWidth: 0 }}>{s.title || '新对话'}</Typography.Text>
              <Space size={4} onClick={(e) => e.stopPropagation()}>
                <Popconfirm
                  title="删除会话？"
                  description="删除后会移入回收站，30 天后自动清理"
                  onConfirm={async () => {
                    await confirmDeleteSession(s)
                  }}
                >
                  <Button size="small" danger type="text" icon={<DeleteOutlined />} />
                </Popconfirm>
              </Space>
            </div>
          ))}
          {sessions.length === 0 && (
            <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 24, fontSize: 12 }}>暂无会话</div>
          )}
        </div>
        {/* 固定底栏：回收站入口 */}
        <div style={{ flexShrink: 0, paddingTop: 8, marginTop: 8, borderTop: `1px solid ${PALETTE.border}` }}>
          <Button
            size="small" block icon={<DeleteOutlined />}
            type={view === 'trash' ? 'primary' : 'default'}
            onClick={openTrash}
          >
            回收站
          </Button>
        </div>
      </div>

      {/* 主工作区：对话 或 回收站 */}
      {view === 'trash' ? (
        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', background: '#FFFFFF' }}>
          <div style={{
            padding: '8px 12px', borderBottom: `1px solid ${PALETTE.border}`,
            display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0,
          }}>
            <Button size="small" icon={<ArrowLeftOutlined />} onClick={backToChat}>返回对话</Button>
            <span style={{ fontWeight: 600, fontSize: 14, color: PALETTE.text }}>回收站</span>
            <span style={{ fontSize: 12, color: PALETTE.textTertiary }}>30 天后自动清理</span>
          </div>
          <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', padding: 12 }}>
            {trash.length === 0 ? (
              <div style={{ textAlign: 'center', color: PALETTE.textTertiary, paddingTop: 80 }}>
                <DeleteOutlined style={{ fontSize: 28 }} />
                <div style={{ marginTop: 8 }}>回收站是空的</div>
              </div>
            ) : trash.map((item) => (
              <div
                key={item.id}
                style={{
                  display: 'flex', alignItems: 'center', gap: 8,
                  border: `1px solid ${PALETTE.border}`, borderRadius: 6,
                  padding: '8px 10px', marginBottom: 8,
                }}
              >
                <div style={{ flex: 1, minWidth: 0 }}>
                  <Typography.Text ellipsis style={{ fontSize: 13 }}>{item.title || '新对话'}</Typography.Text>
                  <div style={{ fontSize: 12, color: PALETTE.textTertiary, marginTop: 2 }}>
                    {item.message_count} 条消息 · 删除于 {formatTime(item.deleted_at)}
                  </div>
                </div>
                <Popconfirm
                  title="彻底删除后不可恢复"
                  okText="彻底删除"
                  okButtonProps={{ danger: true }}
                  onConfirm={() => void purgeTrash(item.id)}
                >
                  <Button size="small" danger icon={<DeleteOutlined />}>彻底删除</Button>
                </Popconfirm>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', background: '#FFFFFF' }}>
          <div style={{
            flexShrink: 0, padding: '6px 16px', borderBottom: `1px solid ${PALETTE.border}`,
            display: 'flex', alignItems: 'center', gap: 8,
          }}>
            <Tag color={projectId ? 'blue' : 'default'} style={{ margin: 0 }}>
              <ProjectOutlined /> {projectName || '未选择项目'}
            </Tag>
            <Tag color={envId ? 'green' : 'default'} style={{ margin: 0 }}>
              <EnvironmentOutlined /> {envName || '未选择环境'}
            </Tag>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              Copilot 把以上选择作为「当前项目/环境」，相关工具缺省参数自动生效
            </Typography.Text>
          </div>
          <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', padding: '12px 16px' }}>
            {messages.length === 0 && (
              <div style={{ textAlign: 'center', color: PALETTE.textTertiary, marginTop: 40 }}>
                <RobotOutlined style={{ fontSize: 28 }} />
                <div style={{ marginTop: 8 }}>向 Copilot 描述任务，例如「当前项目有哪些接口」「创建一个接口 GET /ping」</div>
              </div>
            )}
            {messages.map((m) => (
              <div
                key={m.id}
                style={{
                  display: 'flex', justifyContent: m.role === 'user' ? 'flex-end' : 'flex-start',
                  marginBottom: 12,
                }}>
                <div style={{
                  maxWidth: '78%',
                  padding: '8px 12px',
                  borderRadius: 10,
                  background: m.role === 'user' ? PALETTE.primary : '#FFFFFF',
                  color: m.role === 'user' ? '#FFFFFF' : PALETTE.text,
                  border: m.role === 'user' ? undefined : `1px solid ${PALETTE.border}`,
                  boxShadow: '0 1px 2px rgba(0,0,0,.04)',
                }}>
                  {m.parts.map((p: any, i: number) => (
                    <PartView key={i} part={p} onRespond={respond} />
                  ))}
                </div>
              </div>
            ))}
            <div ref={bottomRef} />
          </div>
          <div style={{ padding: '8px 12px', borderTop: `1px solid ${PALETTE.border}`, flexShrink: 0 }}>
            <Space.Compact style={{ width: '100%' }}>
              <Input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onPressEnter={submit}
                placeholder="输入消息，Enter 发送"
              />
              {busy ? (
                <Button danger icon={<StopOutlined />} onClick={stop} title="停止生成" />
              ) : (
                <Button type="primary" icon={<SendOutlined />} onClick={submit} disabled={!input.trim()} />
              )}
            </Space.Compact>
          </div>
        </div>
      )}
    </div>
  )
}

function formatTime(v: string) {
  if (!v) return ''
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleString('zh-CN', { hour12: false })
}


// 单条 UIMessagePart 渲染：text / reasoning / tool（含审批态）
function PartView({ part, onRespond }: { part: any; onRespond: (p: any, ok: boolean) => void }) {
  if (part.type === 'text') {
    // 用 span 继承气泡颜色（用户蓝底白字 / AI 白底深字）；Typography 自带色会覆盖继承
    return <span style={{ whiteSpace: 'pre-wrap' }}>{part.text}</span>
  }
  if (part.type === 'reasoning') {
    return (
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, whiteSpace: 'pre-wrap' }} ellipsis={{ expandable: true, symbol: '思考过程' }}>
        {part.text}
      </Typography.Paragraph>
    )
  }
  // 工具 part：静态工具 type 形如 tool-<name>，动态工具为 dynamic-tool + toolName
  const toolName = part.type === 'dynamic-tool' ? part.toolName : String(part.type).replace(/^tool-/, '')
  if (part.type === 'dynamic-tool' || String(part.type).startsWith('tool-')) {
    return (
      <div style={{ border: `1px solid ${PALETTE.border}`, borderRadius: 6, padding: 8, margin: '4px 0', fontSize: 12 }}>
        <Space>
          <Tag color="blue">{toolName}</Tag>
          <StateTag state={part.state} />
        </Space>
        {part.input != null && (
          <pre style={{ margin: '4px 0', maxHeight: 120, overflow: 'auto' }}>
            {typeof part.input === 'string' ? part.input : JSON.stringify(part.input, null, 1)}
          </pre>
        )}
        {part.state === 'approval-requested' && (
          <Space>
            <Button size="small" type="primary" onClick={() => onRespond(part, true)}>批准执行</Button>
            <Button size="small" danger onClick={() => onRespond(part, false)}>拒绝</Button>
          </Space>
        )}
        {part.state === 'approval-responded' && <Typography.Text type="secondary">已审批，等待执行…</Typography.Text>}
        {part.state === 'output-available' && part.output != null && (
          <pre style={{ margin: '4px 0', maxHeight: 120, overflow: 'auto' }}>
            {typeof part.output === 'string' ? part.output : JSON.stringify(part.output, null, 1)}
          </pre>
        )}
        {part.state === 'output-error' && <Typography.Text type="danger">{part.errorText}</Typography.Text>}
      </div>
    )
  }
  return null
}

function StateTag({ state }: { state?: string }) {
  const map: Record<string, [string, string]> = {
    'input-streaming': ['default', '参数生成中'],
    'input-available': ['default', '待调用'],
    'approval-requested': ['orange', '待审批'],
    'approval-responded': ['gold', '已审批'],
    'output-available': ['green', '已完成'],
    'output-error': ['red', '失败'],
  }
  const [color, label] = map[state || ''] || ['default', state || '']
  return <Tag color={color}>{label}</Tag>
}
