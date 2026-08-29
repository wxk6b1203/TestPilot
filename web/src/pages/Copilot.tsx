// Copilot 对话页：Vercel AI SDK v7（ai + @ai-sdk/react useChat）消费 SSE + 写操作 HITL 审批。
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { KeyboardEvent, UIEvent } from 'react'
import { Button, Input, Listy, Pagination, Popconfirm, Space, Spin, Tag, Typography } from 'antd'
import {
  ArrowLeftOutlined, ClockCircleOutlined, CopyOutlined, DeleteOutlined, EnvironmentOutlined,
  LoadingOutlined, PlusOutlined, ProjectOutlined, RobotOutlined, SendOutlined, StopOutlined,
} from '@ant-design/icons'
import { useChat } from '@ai-sdk/react'
import { DefaultChatTransport, lastAssistantMessageIsCompleteWithApprovalResponses } from 'ai'
import type { UIMessage } from 'ai'
import { del, get, getToken } from '../api'
import type { ListResp } from '../api'
import { useLayout } from '../hooks/useLayout'
import MarkdownView from '../components/MarkdownView'
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

// 空态快捷示例：让新用户一眼看到 Copilot 的能力入口（含 Playwright UI 用例生成）
const SUGGESTIONS = [
  '当前项目有哪些接口',
  '帮我分析最近一次失败的运行',
  '生成一个打开当前环境首页并断言欢迎语的 Playwright UI 用例',
]

// 前端流空闲看门狗：SSE 长时间无任何增量（首 token 慢 / 供应商卡住）时
// 主动 abort，避免 busy 状态永久占用页面。HITL 等待审批期间不启动该计时。
const STREAM_IDLE_TIMEOUT_MS = 120_000
const STREAM_IDLE_TIMEOUT_LABEL = '2 分钟'
// 流式 UI 更新节流：长回复按 100ms 批量渲染，降低 Markdown 反复解析的卡顿
const STREAM_UI_THROTTLE_MS = 100
// 与 Copilot 后端 _MAX_CHAT_BODY 对齐：发送前按 UTF-8 字节数预检，避免
// 大会话历史先扣 AI 配额再被 413 拒绝
const MAX_CHAT_BODY_BYTES = 1 << 20

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

// AI SDK 对非流式错误（如 413）会直接把响应体原样放进 message，
// 这里把常见错误转成用户可读的中文提示。
function describeChatError(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e)
  if (raw.includes('body too large') || raw.includes('> 1048576')) {
    return '对话历史过长，已超过 Copilot 请求上限（1MB），请点击「新会话」开始新的对话'
  }
  try {
    const obj = JSON.parse(raw)
    if (typeof obj?.error === 'string') return obj.error
    if (typeof obj?.error?.message === 'string') return obj.error.message
    if (typeof obj?.errorText === 'string') return obj.errorText
  } catch {
    // 不是 JSON，按原文展示
  }
  return raw
}

// 一个消息组（相邻同角色消息合成一个气泡）可复制的纯文本：
// 只取 text parts，reasoning/工具卡不混入，用户复制到的就是对话正文。
function copyableTextOfGroup(group: { items: UIMessage[] }): string {
  return group.items
    .flatMap((m) => m.parts)
    .filter((p: any) => p.type === 'text')
    .map((p: any) => String(p.text ?? ''))
    .filter((s) => s.trim())
    .join('\n\n')
}

// Clipboard API 优先；非安全上下文或写入失败时退回 execCommand 方案
async function copyText(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // fallthrough
    }
  }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.appendChild(ta)
  ta.select()
  const ok = document.execCommand('copy')
  document.body.removeChild(ta)
  if (!ok) throw new Error('copy failed')
}

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
  const [sessionsTotal, setSessionsTotal] = useState(0)
  const [sessionsLoading, setSessionsLoading] = useState(false)
  const sessionsLoadingRef = useRef(false)
  const sessionsPageRef = useRef(0) // 已加载到的页码
  const [view, setView] = useState<'chat' | 'trash'>('chat')
  const [trash, setTrash] = useState<TrashSession[]>([])
  const [trashTotal, setTrashTotal] = useState(0)
  const [trashPage, setTrashPage] = useState(1)
  const [trashPageSize, setTrashPageSize] = useState(10)
  const [input, setInput] = useState('')
  const bottomRef = useRef<HTMLDivElement>(null)
  // 多行输入框：修饰键换行时手工插 \n，随后恢复光标位置
  const inputRef = useRef<any>(null)
  const pendingCursorRef = useRef<number | null>(null)
  useLayoutEffect(() => {
    const pos = pendingCursorRef.current
    if (pos == null) return
    pendingCursorRef.current = null
    const el = inputRef.current?.nativeElement ?? inputRef.current
    if (el instanceof HTMLTextAreaElement) {
      el.selectionStart = el.selectionEnd = pos
    }
  }, [input])

  // 会话列表：Listy 无限加载（滚动近底部追加下一页，virtual 渲染视口内行）
  const loadSessions = (reset = false) => {
    if (sessionsLoadingRef.current) return Promise.resolve()
    const page = reset ? 1 : sessionsPageRef.current + 1
    sessionsLoadingRef.current = true
    setSessionsLoading(true)
    return get<ListResp<Session>>(`/api/v1/copilot/sessions?page=${page}&page_size=20`)
      .then((r) => {
        sessionsPageRef.current = page
        setSessionsTotal(r.total ?? 0)
        setSessions((prev) => (reset ? r.items ?? [] : [...prev, ...(r.items ?? [])]))
      })
      .catch((e: any) => message.error(e.message))
      .finally(() => {
        sessionsLoadingRef.current = false
        setSessionsLoading(false)
      })
  }

  // Listy 需要像素高度：ResizeObserver 跟踪侧栏列表容器
  const sessionListRef = useRef<HTMLDivElement>(null)
  const [sessionListH, setSessionListH] = useState(0)
  useLayoutEffect(() => {
    const el = sessionListRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setSessionListH(el.clientHeight))
    ro.observe(el)
    setSessionListH(el.clientHeight)
    return () => ro.disconnect()
  }, [])

  const onSessionsScroll = (e: UIEvent<HTMLElement>) => {
    const { scrollTop, clientHeight, scrollHeight } = e.currentTarget
    if (scrollHeight - scrollTop - clientHeight > 80) return
    if (sessions.length < sessionsTotal) void loadSessions()
  }

  // 回收站：常规服务端分页（Pagination 翻页器）
  const loadTrash = (page = trashPage, size = trashPageSize) =>
    get<ListResp<TrashSession>>(`/api/v1/copilot/trash?page=${page}&page_size=${size}`)
      .then((r) => {
        setTrash(r.items ?? [])
        setTrashTotal(r.total ?? 0)
      })
      .catch((e: any) => message.error(e.message))

  const openTrash = () => {
    if (busy) stop()
    setView('trash')
    setTrashPage(1)
    loadTrash(1)
  }

  const backToChat = () => setView('chat')

  const confirmDeleteSession = async (s: Session) => {
    try {
      await del(`/api/v1/copilot/sessions/${s.id}`)
      message.success('已移入回收站')
      setSessions((prev) => prev.filter((x) => x.id !== s.id))
      setSessionsTotal((t) => Math.max(0, t - 1))
      if (sessionId === s.id) newChat()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const purgeTrash = async (id: string) => {
    try {
      await del(`/api/v1/copilot/trash/${id}`)
      message.success('已彻底删除')
      const left = trashTotal - 1
      const maxPage = Math.max(1, Math.ceil(left / trashPageSize))
      const next = Math.min(trashPage, maxPage)
      if (next !== trashPage) setTrashPage(next)
      await loadTrash(next)
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
        prepareSendMessagesRequest: ({ id, messages, body, headers }) => {
          const nextBody = { ...body, id, messages: sanitizeMessages(messages) }
          // 与后端 1MB 限制对齐：超限直接本地报错，不走网络/不扣 AI 配额
          const bytes = new TextEncoder().encode(JSON.stringify(nextBody)).length
          if (bytes > MAX_CHAT_BODY_BYTES) {
            throw new Error(
              `对话历史过长（${Math.ceil(bytes / 1024)}KB > 1MB），无法继续发送。请点击「新会话」开始新的对话`,
            )
          }
          return { headers, body: nextBody }
        },
        // 拦截响应头：首次响应携带 X-Session-Id，记下后续续聊沿用
        fetch: async (url, init) => {
          const res = await globalThis.fetch(url, init)
          const sid = res.headers.get('X-Session-Id')
          if (sid && sid !== sessionIdRef.current) {
            sessionIdRef.current = sid
            setSessionId(sid)
            loadSessions(true)
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
    throttle: STREAM_UI_THROTTLE_MS,
    sendAutomaticallyWhen: ({ messages: ms }) =>
      lastAssistantMessageIsCompleteWithApprovalResponses({ messages: ms }),
    onError: (e) => message.error(describeChatError(e)),
  })

  const busy = status === 'submitted' || status === 'streaming'

  // 等待 HITL 审批时停表：这不是「卡住」，由用户决定何时批准/拒绝
  const waitingApproval = useMemo(() => {
    const last = messages[messages.length - 1]
    return last?.role === 'assistant' &&
      last.parts.some((p: any) => p.state === 'approval-requested')
  }, [messages])

  // 空闲看门狗：busy 期间超过 STREAM_IDLE_TIMEOUT_MS 没有任何消息增量，
  // 主动 stop() 让页面回到可操作状态（stop 会 abort fetch，SDK 按取消处理）。
  // 只写 ref，不 setState：避免秒级计时让整个 Copilot 页反复重渲染。
  const lastActivityAtRef = useRef(Date.now())
  const watchdogFiredRef = useRef(false)
  useEffect(() => {
    lastActivityAtRef.current = Date.now()
  }, [messages])
  useEffect(() => {
    if (!busy || waitingApproval) return
    watchdogFiredRef.current = false
    const timer = window.setInterval(() => {
      if (!watchdogFiredRef.current &&
          Date.now() - lastActivityAtRef.current >= STREAM_IDLE_TIMEOUT_MS) {
        watchdogFiredRef.current = true
        stop()
        message.error(
          `Copilot 超过 ${STREAM_IDLE_TIMEOUT_LABEL}没有新输出，已自动停止，请缩短请求或重试`,
        )
      }
    }, 1000)
    return () => window.clearInterval(timer)
  }, [busy, waitingApproval, stop])

  // 相邻同角色消息合成一个外框：一次 Agent 往返产生的多个 UIMessage
  // （文本 + 工具卡 + 审批后的续接）在流式与刷新后的历史里都显示为同一气泡
  const messageGroups = useMemo(() => {
    const groups: { role: string; items: typeof messages }[] = []
    for (const m of messages) {
      const role = m.role === 'user' ? 'user' : 'assistant'
      const last = groups[groups.length - 1]
      if (last && last.role === role) {
        last.items.push(m)
      } else {
        groups.push({ role, items: [m] })
      }
    }
    return groups
  }, [messages])

  useEffect(() => {
    loadSessions(true)
  }, [])
  useEffect(() => {
    // 流式期间按帧滚动；用户向上翻阅历史（离底部 >120px）时不抢滚动位置
    const frame = window.requestAnimationFrame(() => {
      const el = bottomRef.current
      const scroller = el?.parentElement
      if (scroller && scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight > 120) {
        return
      }
      el?.scrollIntoView({ block: 'end', behavior: 'auto' })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [messages])

  // 加载历史会话（这些 UIMessage 既用于展示，也会作为下一次请求的对话上下文）
  const openSession = async (sid: string) => {
    if (busy) stop() // 流式中直接覆盖 messages 会与 SDK 写入竞争，先中止
    setView('chat')
    try {
      const r = await get<ListResp<any>>(`/api/v1/copilot/sessions/${sid}/messages`)
      const msgs: UIMessage[] = []
      // 持久化把 assistant 的工具调用与其结果分成 role=2 / role=3 两行；
      // 这里按 tool_call_id（旧数据无 id 时按工具名 FIFO）合并回同一个
      // dynamic-tool part。绝不能把行 ID 当 toolCallId：一行多个工具调用会
      // 产生重复 tool_call_id，第二次发消息时 DeepSeek 直接 400。
      const pendingCalls = new Map<string, any>()
      const pendingByName = new Map<string, any[]>()
      const mergeResult = (tc: any) => {
        const part = (tc.tool_call_id && pendingCalls.get(tc.tool_call_id)) ||
          pendingByName.get(tc.name)?.shift()
        if (part) {
          part.state = 'output-available'
          part.output = tc.result
          return true
        }
        return false
      }
      for (const m of r.items) {
        const calls = JSON.parse(m.tool_calls || '[]')
        if (m.role === 1) {
          msgs.push({ id: String(m.id), role: 'user', parts: [{ type: 'text', text: m.content }] })
          continue
        }
        if (m.role === 3) {
          // 工具结果行：合并回前面待输出的调用；找不到配对（旧数据孤行）才单独展示
          const tc = calls[0]
          if (tc && !mergeResult(tc)) {
            msgs.push({
              id: String(m.id), role: 'assistant',
              parts: [{
                type: 'dynamic-tool', toolName: tc.name, toolCallId: `${m.id}:0`,
                state: 'output-available', input: null, output: tc.result,
              }],
            })
          }
          continue
        }
        const parts: any[] = []
        if (m.content) parts.push({ type: 'text', text: m.content })
        calls.forEach((tc: any, idx: number) => {
          const toolCallId = tc.tool_call_id || `${m.id}:${idx}`
          if (tc.result === undefined || tc.result === null) {
            // 后端 schema：input-available 只需要 input；result 稍后按 ID 合并
            const part = {
              type: 'dynamic-tool', toolName: tc.name, toolCallId,
              state: 'input-available', input: tc.args ?? null,
            }
            parts.push(part)
            pendingCalls.set(toolCallId, part)
            if (!tc.tool_call_id) {
              pendingByName.set(tc.name, [...(pendingByName.get(tc.name) || []), part])
            }
          } else {
            parts.push({
              type: 'dynamic-tool', toolName: tc.name, toolCallId,
              state: 'output-available', input: tc.args ?? null, output: tc.result,
            })
          }
        })
        msgs.push({ id: String(m.id), role: 'assistant', parts })
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

  const submitText = (text: string) => {
    const trimmed = text.trim()
    if (!trimmed || busy) return
    setInput('')
    sendMessage({ text: trimmed })
  }

  const submit = () => submitText(input)

  // Enter 发送；Shift/Cmd/Ctrl + Enter 手动插入换行并保持光标位置。
  // 中文输入法组合态（isComposing）下的 Enter 不处理，避免误发送。
  const handleChatKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
    if (e.shiftKey || e.metaKey || e.ctrlKey) {
      e.preventDefault()
      const el = (inputRef.current?.nativeElement ?? inputRef.current ?? e.currentTarget)
      const start = el.selectionStart ?? input.length
      const end = el.selectionEnd ?? input.length
      pendingCursorRef.current = start + 1
      setInput(`${input.slice(0, start)}\n${input.slice(end)}`)
      return
    }
    e.preventDefault()
    submit()
  }

  // HITL：批准/拒绝 → 补审批回执，sendAutomaticallyWhen 命中后 SDK 自动续发
  // useCallback 保持引用稳定：PartView 用 memo 后，未变化的工具卡不会随文本流重渲染
  const respond = useCallback((part: any, approved: boolean) => {
    if (!part.approval?.id) return
    addToolApprovalResponse({ id: part.approval.id, approved })
  }, [addToolApprovalResponse])

  const handleCopy = async (text: string) => {
    try {
      await copyText(text)
      message.success('已复制')
    } catch {
      message.error('复制失败，请手动选择文本')
    }
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
        <div ref={sessionListRef} style={{ flex: 1, minHeight: 0 }}>
          {sessions.length === 0 && !sessionsLoading ? (
            <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 24, fontSize: 12 }}>暂无会话</div>
          ) : (
            <Listy<Session>
              virtual={sessionListH > 0}
              height={sessionListH || undefined}
              items={sessions}
              rowKey="id"
              styles={{ item: { padding: '6px 8px', background: 'transparent', borderBottom: 'none' } }}
              itemRender={(item) => (
                <div
                  onClick={() => openSession(item.id)}
                  style={{
                    cursor: 'pointer', borderRadius: 6,
                    display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8,
                    background: item.id === sessionId ? PALETTE.selectedRow : undefined,
                  }}
                >
                  <Typography.Text ellipsis style={{ fontSize: 13, flex: 1, minWidth: 0 }}>{item.title || '新对话'}</Typography.Text>
                  <Space size={4} onClick={(e) => e.stopPropagation()}>
                    <Popconfirm
                      title="删除会话？"
                      description="删除后会移入回收站，30 天后自动清理"
                      onConfirm={async () => {
                        await confirmDeleteSession(item)
                      }}
                    >
                      <Button size="small" danger type="text" icon={<DeleteOutlined />} />
                    </Popconfirm>
                  </Space>
                </div>
              )}
              onScroll={onSessionsScroll}
            />
          )}
        </div>
        {sessionsLoading && (
          <div style={{ flexShrink: 0, textAlign: 'center', padding: '4px 0' }}>
            <Spin size="small" />
          </div>
        )}
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
            ) : (<>
              {trash.map((item) => (
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
              </>
            )}
          </div>
          {trashTotal > 0 && (
            <div style={{ flexShrink: 0, padding: '8px 12px', borderTop: `1px solid ${PALETTE.border}`, display: 'flex', justifyContent: 'flex-end' }}>
              <Pagination
                size="small" current={trashPage} pageSize={trashPageSize} total={trashTotal}
                showSizeChanger pageSizeOptions={[10, 20, 50]}
                showTotal={(t) => `共 ${t} 条`}
                onChange={(pg, ps) => {
                  setTrashPage(pg)
                  setTrashPageSize(ps)
                  loadTrash(pg, ps)
                }}
              />
            </div>
          )}
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
                  <div style={{
                    marginTop: 16, display: 'flex', flexWrap: 'wrap', gap: 8,
                    justifyContent: 'center', padding: '0 24px',
                  }}>
                    {SUGGESTIONS.map((s) => (
                      <Button key={s} size="small" onClick={() => submitText(s)}>
                        {s}
                      </Button>
                    ))}
                  </div>
              </div>
            )}
            {messageGroups.map((group) => {
              const isUser = group.role === 'user'
              const copyableText = copyableTextOfGroup(group)
              return (
                <div
                  key={group.items[0].id}
                  style={{
                    display: 'flex', justifyContent: isUser ? 'flex-end' : 'flex-start',
                    marginBottom: 12,
                  }}>
                  <div style={{
                    display: 'flex', flexDirection: 'column',
                    alignItems: isUser ? 'flex-end' : 'flex-start',
                    maxWidth: '78%',
                  }}>
                    <div style={{
                      padding: '8px 12px',
                      borderRadius: 10,
                      background: isUser ? PALETTE.primary : '#FFFFFF',
                      color: isUser ? '#FFFFFF' : PALETTE.text,
                      border: isUser ? undefined : `1px solid ${PALETTE.border}`,
                      boxShadow: '0 1px 2px rgba(0,0,0,.04)',
                    }}>
                      {group.items.map((m) => (
                        m.parts.map((p: any, i: number) => (
                          <PartView key={`${m.id}:${i}`} part={p} role={group.role} onRespond={respond} />
                        ))
                      ))}
                    </div>
                    {copyableText && (
                      <Button
                        type="text"
                        size="small"
                        icon={<CopyOutlined />}
                        aria-label="复制消息"
                        title="复制消息"
                        onClick={() => void handleCopy(copyableText)}
                        style={{
                          marginTop: 4, height: 22, width: 22, padding: 0,
                          fontSize: 12, color: PALETTE.textTertiary,
                        }}
                      />
                    )}
                  </div>
                </div>
              )
            })}
            <div ref={bottomRef} />
          </div>
          <div style={{ padding: '8px 12px', borderTop: `1px solid ${PALETTE.border}`, flexShrink: 0 }}>
            {busy && <BusyIndicator idleTimeoutLabel={STREAM_IDLE_TIMEOUT_LABEL} />}
            <div style={{ display: 'flex', alignItems: 'flex-end', gap: 8 }}>
              <Input.TextArea
                ref={inputRef}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleChatKeyDown}
                autoSize={{ minRows: 1, maxRows: 8 }}
                placeholder="输入消息，Enter 发送；Shift/Cmd/Ctrl + Enter 换行"
                style={{ flex: 1, resize: 'none' }}
              />
              {busy ? (
                <Button danger icon={<StopOutlined />} onClick={stop} title="停止生成" style={{ flexShrink: 0 }} />
              ) : (
                <Button type="primary" icon={<SendOutlined />} onClick={submit}
                  disabled={!input.trim()} style={{ flexShrink: 0 }} />
              )}
            </div>
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

// busy 期间的状态条：计时状态放在独立组件里，避免每秒触发 Copilot 整页重渲染
function BusyIndicator({ idleTimeoutLabel }: { idleTimeoutLabel: string }) {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    const startedAt = Date.now()
    const timer = window.setInterval(() => {
      setSeconds(Math.floor((Date.now() - startedAt) / 1000))
    }, 1000)
    return () => window.clearInterval(timer)
  }, [])
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 8,
      padding: '2px 0 6px', fontSize: 12, color: PALETTE.textSecondary,
    }}>
      <LoadingOutlined spin style={{ color: PALETTE.primary }} />
      <span>Copilot 正在处理，已等待 {seconds}s</span>
      <span style={{ color: PALETTE.textTertiary }}>
        · 连续 {idleTimeoutLabel}无新输出会自动停止
      </span>
      <ClockCircleOutlined />
    </div>
  )
}


// 整段 JSON 检测：工具返回/模型直接输出的 JSON 保持 pre + prettify，
// 避免被 Markdown 当段落渲染后丢失缩进与换行
const MONO = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace'

function parseJSONObject(text: string): object | null {
  const trimmed = text.trim()
  if (!trimmed || (trimmed[0] !== '{' && trimmed[0] !== '[')) return null
  try {
    const value = JSON.parse(trimmed)
    if (value !== null && typeof value === 'object') return value as object
  } catch {
    // 非 JSON
  }
  return null
}

// 工具返回/模型输出可能非常大：显示层先截断再做 JSON 解析/Markdown，
// 避免一次 JSON.stringify/DOMPurify 把主线程卡死。完整数据仍由服务端持久化。
const MAX_TOOL_VALUE_CHARS = 20_000
const MAX_RICH_TEXT_CHARS = 200_000

function truncateForDisplay(text: string, max: number): string {
  if (text.length <= max) return text
  return `${text.slice(0, max)}\n…[内容过长，显示已截断，共 ${text.length} 字符]`
}

// 工具卡 input/output 的显示值：
// - JSON 字符串（常见于 Vercel AI 工具 args/result）→ parse 后按 1 空格缩进 prettify
// - 普通字符串（错误信息等）→ 原文
// - 对象/数组 → JSON.stringify prettify
function stringifyToolValue(value: unknown): string {
  if (typeof value === 'string') {
    if (value.length > MAX_TOOL_VALUE_CHARS) return truncateForDisplay(value, MAX_TOOL_VALUE_CHARS)
    const parsed = parseJSONObject(value)
    if (parsed !== null) return JSON.stringify(parsed, null, 1)
    return value
  }
  const text = JSON.stringify(value, null, 1) ?? String(value)
  return truncateForDisplay(text, MAX_TOOL_VALUE_CHARS)
}

function prettifyJSONText(text: string): string | null {
  const value = parseJSONObject(text)
  return value === null ? null : JSON.stringify(value, null, 2)
}

// 单条 UIMessagePart 渲染：text / reasoning / tool（含审批态）。
// memo：文本流每个 delta 都会产生新 message 快照，但未变化的工具卡应跳过重渲染。
const PartView = memo(function PartView({ part, role, onRespond }: {
  part: any
  role: string
  onRespond: (p: any, ok: boolean) => void
}) {
  if (part.type === 'text') {
    const text = String(part.text ?? '')
    if (role === 'assistant') {
      const preStyle = {
        margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
        fontFamily: MONO, fontSize: 12, textAlign: 'left',
      } as const
      // 超长回复不再走 marked/DOMPurify（同步解析会冻结主线程），直接按纯文本展示
      if (text.length > MAX_RICH_TEXT_CHARS) {
        return <pre style={preStyle}>{text}</pre>
      }
      // 整段 JSON：沿用工具返回的 pre/prettify 呈现；否则 LLM 文本按 Markdown 渲染
      const pretty = prettifyJSONText(text)
      if (pretty !== null) {
        return <pre style={preStyle}>{pretty}</pre>
      }
      return <MarkdownView text={text} />
    }
    // 用户输入保持纯文本，避免把用户敲的 # 等误渲染
    // 用 span 继承气泡颜色（用户蓝底白字）；Typography 自带色会覆盖继承
    return <span style={{ whiteSpace: 'pre-wrap' }}>{text}</span>
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
            {stringifyToolValue(part.input)}
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
            {stringifyToolValue(part.output)}
          </pre>
        )}
        {part.state === 'output-error' && <Typography.Text type="danger">{part.errorText}</Typography.Text>}
      </div>
    )
  }
  return null
})

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
