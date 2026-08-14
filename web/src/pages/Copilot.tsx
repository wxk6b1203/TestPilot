// Copilot 对话页：Vercel AI SSE 协议手写消费 + 写操作 HITL 审批。
import { useEffect, useRef, useState } from 'react'
import { Button, Input, List, Space, Tag, Typography, message } from 'antd'
import { RobotOutlined, SendOutlined, PlusOutlined } from '@ant-design/icons'
import { get, getToken } from '../api'
import type { ListResp } from '../api'
import { PALETTE } from '../theme'

interface UIPart {
  type: string
  text?: string
  toolCallId?: string
  toolName?: string
  state?: string
  input?: any
  output?: any
  errorText?: string
  approval?: { id: string; approved?: boolean }
}
interface UIMessage {
  id: string
  role: 'user' | 'assistant'
  parts: UIPart[]
}
interface Session {
  id: string
  title: string
  created_at: string
}

let seq = 0
const nid = () => `m${Date.now()}-${seq++}`

export default function Copilot() {
  const [sessionId, setSessionId] = useState('')
  const [sessions, setSessions] = useState<Session[]>([])
  const [messages, setMessages] = useState<UIMessage[]>([])
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const bottomRef = useRef<HTMLDivElement>(null)
  const chatId = useRef(`chat-${Date.now()}`)

  const loadSessions = () =>
    get<ListResp<Session>>('/api/v1/copilot/sessions?page_size=50').then((r) => setSessions(r.items))
  useEffect(() => {
    loadSessions()
  }, [])
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  // 加载历史会话（仅展示：user/assistant 文本 + 工具摘要）
  const openSession = async (sid: string) => {
    const r = await get<ListResp<any>>(`/api/v1/copilot/sessions/${sid}/messages`)
    const msgs: UIMessage[] = []
    for (const m of r.items) {
      if (m.role === 1) {
        msgs.push({ id: String(m.id), role: 'user', parts: [{ type: 'text', text: m.content }] })
      } else {
        const parts: UIPart[] = []
        if (m.content) parts.push({ type: 'text', text: m.content })
        for (const tc of JSON.parse(m.tool_calls || '[]')) {
          parts.push({
            type: `tool-${tc.name}`, toolName: tc.name, toolCallId: String(m.id),
            state: 'output-available', input: tc.args, output: tc.result,
          })
        }
        msgs.push({ id: String(m.id), role: 'assistant', parts })
      }
    }
    setSessionId(sid)
    setMessages(msgs)
    chatId.current = `chat-${sid}`
  }

  const newChat = () => {
    setSessionId('')
    setMessages([])
    chatId.current = `chat-${Date.now()}`
  }

  // 发送（或审批后重发）：POST 全量 messages。
  // mergeInto 指定时把流事件合并进既有 assistant 消息（审批续跑），否则新建一条。
  const send = async (msgs: UIMessage[], mergeInto?: string) => {
    setBusy(true)
    try {
      const res = await fetch('/copilot-api/chat', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${getToken()}`,
          ...(sessionId ? { 'X-Session-Id': sessionId } : {}),
        },
        body: JSON.stringify({ trigger: 'submit-message', id: chatId.current, messages: msgs }),
      })
      if (!res.ok) throw new Error(`copilot HTTP ${res.status}: ${(await res.text()).slice(0, 200)}`)
      const sid = res.headers.get('X-Session-Id')
      if (sid && sid !== sessionId) {
        setSessionId(sid)
        loadSessions()
      }
      let targetId = mergeInto
      if (!targetId) {
        const assistant: UIMessage = { id: nid(), role: 'assistant', parts: [] }
        targetId = assistant.id
        setMessages((cur) => [...cur, assistant])
      }
      const mid = targetId
      await consumeSSE(res, (ev) => applyEvent(mid, ev))
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setBusy(false)
    }
  }

  // 把 SSE 事件合并进 assistant 消息
  const applyEvent = (mid: string, ev: any) => {
    setMessages((cur) =>
      cur.map((m) => {
        if (m.id !== mid) return m
        const parts = [...m.parts]
        // 事件可能先于 tool-input-start 到达：按 toolCallId 找，否则就地新建
        const toolFor = (ev: any): UIPart => {
          let t = parts.find((p) => p.toolCallId === ev.toolCallId)
          if (!t) {
            t = { type: `tool-${ev.toolName || 'unknown'}`, toolName: ev.toolName, toolCallId: ev.toolCallId }
            parts.push(t)
          }
          return t
        }
        switch (ev.type) {
          case 'text-start':
            parts.push({ type: 'text', text: '' })
            break
          case 'text-delta': {
            const t = [...parts].reverse().find((p) => p.type === 'text')
            if (t) t.text = (t.text || '') + ev.delta
            else parts.push({ type: 'text', text: ev.delta })
            break
          }
          case 'reasoning-delta': {
            const t = [...parts].reverse().find((p) => p.type === 'reasoning')
            if (t) t.text = (t.text || '') + ev.delta
            else parts.push({ type: 'reasoning', text: ev.delta })
            break
          }
          case 'tool-input-start':
            parts.push({ type: `tool-${ev.toolName}`, toolName: ev.toolName, toolCallId: ev.toolCallId, state: 'input-streaming' })
            break
          case 'tool-input-available': {
            const t = toolFor(ev)
            t.input = ev.input; t.state = 'input-available'
            break
          }
          case 'tool-approval-request': {
            const t = toolFor(ev)
            t.state = 'approval-requested'; t.approval = { id: ev.approvalId }
            break
          }
          case 'tool-output-available': {
            const t = toolFor(ev)
            t.output = ev.output; t.state = 'output-available'
            break
          }
          case 'tool-output-error': {
            const t = toolFor(ev)
            t.errorText = ev.errorText; t.state = 'output-error'
            break
          }
        }
        return { ...m, parts }
      }),
    )
  }

  const submit = () => {
    const text = input.trim()
    if (!text || busy) return
    setInput('')
    const next = [...messages, { id: nid(), role: 'user' as const, parts: [{ type: 'text', text }] }]
    setMessages(next)
    send(next)
  }

  // HITL：批准/拒绝 → part 就地标记 approval-responded，整体重发并合并回原消息
  const respond = (part: UIPart, approved: boolean) => {
    const owner = messages.find((m) => m.parts.some((p) => p.toolCallId === part.toolCallId))
    const next = messages.map((m) => ({
      ...m,
      parts: m.parts.map((p) =>
        p.toolCallId === part.toolCallId && p.state === 'approval-requested'
          ? { ...p, state: 'approval-responded', approval: { id: part.approval!.id, approved } }
          : p,
      ),
    }))
    setMessages(next)
    send(next, owner?.id)
  }

  return (
    <div style={{ display: 'flex', gap: 16, height: 'calc(100vh - 150px)' }}>
      <div style={{ width: 220, borderRight: `1px solid ${PALETTE.border}`, overflowY: 'auto' }}>
        <Button block icon={<PlusOutlined />} onClick={newChat} style={{ marginBottom: 8 }}>
          新会话
        </Button>
        <List
          size="small"
          dataSource={sessions}
          renderItem={(s) => (
            <List.Item
              onClick={() => openSession(s.id)}
              style={{
                cursor: 'pointer',
                background: s.id === sessionId ? PALETTE.selectedRow : undefined,
                padding: '6px 8px',
              }}
            >
              <Typography.Text ellipsis style={{ fontSize: 13 }}>
                {s.title || '(未命名)'}
              </Typography.Text>
            </List.Item>
          )}
        />
      </div>

      <div style={{ flex: 1, display: 'flex', flexDirection: 'column' }}>
        <div style={{ flex: 1, overflowY: 'auto', padding: '0 8px' }}>
          {messages.length === 0 && (
            <Typography.Text type="secondary">
              <RobotOutlined /> 试试：「列出所有项目」「为接口 /json 生成一个测试用例」「分析最近一次失败的运行」
            </Typography.Text>
          )}
          {messages.map((m) => (
            <div key={m.id} style={{ margin: '12px 0', textAlign: m.role === 'user' ? 'right' : 'left' }}>
              <div
                style={{
                  display: 'inline-block', maxWidth: '85%', textAlign: 'left',
                  background: m.role === 'user' ? '#E8EDFE' : PALETTE.bgLayout,
                  borderRadius: 8, padding: '8px 12px',
                }}
              >
                {m.parts.map((p, i) => <PartView key={i} part={p} onRespond={respond} />)}
              </div>
            </div>
          ))}
          <div ref={bottomRef} />
        </div>
        <Space.Compact style={{ width: '100%' }}>
          <Input
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onPressEnter={submit}
            placeholder="输入消息，Enter 发送"
            disabled={busy}
          />
          <Button type="primary" icon={<SendOutlined />} onClick={submit} loading={busy} />
        </Space.Compact>
      </div>
    </div>
  )
}

function PartView({ part, onRespond }: { part: UIPart; onRespond: (p: UIPart, ok: boolean) => void }) {
  if (part.type === 'text') {
    return <Typography.Text style={{ whiteSpace: 'pre-wrap' }}>{part.text}</Typography.Text>
  }
  if (part.type === 'reasoning') {
    return (
      <Typography.Paragraph type="secondary" style={{ fontSize: 12, whiteSpace: 'pre-wrap' }} ellipsis={{ expandable: true, symbol: '思考过程' }}>
        {part.text}
      </Typography.Paragraph>
    )
  }
  if (part.type.startsWith('tool-')) {
    return (
      <div style={{ border: `1px solid ${PALETTE.border}`, borderRadius: 6, padding: 8, margin: '4px 0', fontSize: 12 }}>
        <Space>
          <Tag color="blue">{part.toolName}</Tag>
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
        {part.state === 'output-available' && part.output != null && (
          <pre style={{ margin: '4px 0', maxHeight: 120, overflow: 'auto', color: PALETTE.success }}>
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

// 逐行解析 SSE（data: {...}\n\n），回调每个 JSON 事件
async function consumeSSE(res: Response, onEvent: (ev: any) => void) {
  const reader = res.body!.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    let idx
    while ((idx = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, idx).trim()
      buf = buf.slice(idx + 1)
      if (!line.startsWith('data:')) continue
      const data = line.slice(5).trim()
      if (data === '[DONE]') return
      try {
        onEvent(JSON.parse(data))
      } catch { /* 忽略非 JSON 行 */ }
    }
  }
}
