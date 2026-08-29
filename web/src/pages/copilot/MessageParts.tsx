// Copilot 消息渲染部件：busy 状态条、单条 UIMessagePart 渲染（文本/思考/工具卡）
// 及其显示层辅助函数。与对话状态无关，只吃 props。
import { memo, useEffect, useState } from 'react'
import { Button, Space, Tag, Typography } from 'antd'
import { ClockCircleOutlined, DownOutlined, LoadingOutlined, RightOutlined } from '@ant-design/icons'
import MarkdownView from '../../components/MarkdownView'
import { PALETTE } from '../../theme'

// busy 期间的状态条：计时状态放在独立组件里，避免每秒触发 Copilot 整页重渲染
export function BusyIndicator({ idleTimeoutLabel }: { idleTimeoutLabel: string }) {
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
export const PartView = memo(function PartView({ part, role, onRespond }: {
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
    return <ToolCard part={part} toolName={toolName} onRespond={onRespond} />
  }
  return null
})

// 工具调用可能带很长的 input/output：默认折叠只留名称+状态，点开看全文；
// 审批与失败态默认展开（需要立即看到按钮/错误）。pre 统一自动换行，
// 长行不再撑出横向滚动条。
const TOOL_PRE_STYLE = {
  margin: '4px 0', maxHeight: 120, overflow: 'auto',
  whiteSpace: 'pre-wrap', wordBreak: 'break-word',
} as const

function ToolCard({ part, toolName, onRespond }: {
  part: any
  toolName: string
  onRespond: (p: any, ok: boolean) => void
}) {
  // 用户未点过按钮时按状态推导默认值：审批/失败展开，其余折叠；
  // 审批态始终强制展开（批准/拒绝按钮必须可见）
  const [userExpanded, setUserExpanded] = useState<boolean | null>(null)
  const expanded = part.state === 'approval-requested' ||
    (userExpanded ?? part.state === 'output-error')

  return (
    <div style={{ border: `1px solid ${PALETTE.border}`, borderRadius: 6, padding: 8, margin: '4px 0', fontSize: 12 }}>
      <Space size={4}>
        <Button
          size="small" type="text"
          icon={expanded ? <DownOutlined /> : <RightOutlined />}
          onClick={() => setUserExpanded(!expanded)}
          title={expanded ? '收起' : '展开'}
          style={{ width: 20, height: 20, padding: 0 }}
        />
        <Tag color="blue">{toolName}</Tag>
        <StateTag state={part.state} />
      </Space>
      {expanded && part.input != null && (
        <pre style={TOOL_PRE_STYLE}>
          {stringifyToolValue(part.input)}
        </pre>
      )}
      {expanded && part.state === 'approval-requested' && (
        <Space>
          <Button size="small" type="primary" onClick={() => onRespond(part, true)}>批准执行</Button>
          <Button size="small" danger onClick={() => onRespond(part, false)}>拒绝</Button>
        </Space>
      )}
      {expanded && part.state === 'approval-responded' && <Typography.Text type="secondary">已审批，等待执行…</Typography.Text>}
      {expanded && part.state === 'output-available' && part.output != null && (
        <pre style={TOOL_PRE_STYLE}>
          {stringifyToolValue(part.output)}
        </pre>
      )}
      {expanded && part.state === 'output-error' && <Typography.Text type="danger">{part.errorText}</Typography.Text>}
    </div>
  )
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
