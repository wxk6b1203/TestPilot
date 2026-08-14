import { useState } from 'react'
import { Button, Empty, Segmented, Table, Tabs, Tag, Tooltip } from 'antd'
import { MenuUnfoldOutlined, RocketOutlined } from '@ant-design/icons'
import { PALETTE } from '../theme'
import type { DebugResult } from '../api'

// 响应面板：状态/耗时 + 响应体（Prettify/原文 + 自动换行开关）/响应头/断言/日志。
export default function ResponsePane({ result, loading }: { result?: DebugResult; loading?: boolean }) {
  const [tab, setTab] = useState('body')
  const [view, setView] = useState<'prettify' | 'raw'>('prettify')
  const [wrap, setWrap] = useState(true)
  if (loading) {
    return <div style={{ color: PALETTE.textSecondary, padding: 24 }}>发送中…</div>
  }
  if (!result || !result.step) {
    return (
      <Empty
        image={<div style={{
          width: 96, height: 96, borderRadius: '50%', background: PALETTE.bgLayout,
          display: 'flex', alignItems: 'center', justifyContent: 'center', margin: '0 auto',
        }}>
          <RocketOutlined style={{ fontSize: 40, color: '#BEC2C7' }} />
        </div>}
        description={<span style={{ color: PALETTE.textSecondary }}>点击「发送」按钮获取响应</span>}
      />
    )
  }
  const s = result.step
  const resp = s.response ?? {}
  const status = resp.status
  const ok = s.status === 2
  const okColor = status >= 200 && status < 400

  // 仅取用户响应体：resp.body 为原始文本（快照里的 status/headers/elapsed_ms 不混入）
  const rawBody: string = (() => {
    const b = resp.body
    if (typeof b === 'string') return b
    if (b === undefined || b === null) return ''
    return JSON.stringify(b) // 后端异常形态兜底
  })()
  const prettyBody: string | null = (() => {
    if (!rawBody) return null
    try {
      return JSON.stringify(JSON.parse(rawBody), null, 2)
    } catch {
      return null // 非 JSON：Prettify 视图下提示
    }
  })()
  const headers = (resp.headers ?? {}) as Record<string, string>
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <Tag color={okColor ? 'success' : 'error'}>{String(status ?? '-')} {String(status ?? '')}</Tag>
        <span style={{ color: PALETTE.textSecondary }}>{s.duration_ms}ms</span>
        {!ok && <span style={{ color: '#F54A45' }}>{result.error || '请求失败'}</span>}
      </div>
      <Tabs
        size="small"
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'body',
            label: '响应体',
            children: rawBody === '' ? (
              <span style={{ color: PALETTE.textTertiary }}>（空响应体）</span>
            ) : (
              <div>
                <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6 }}>
                  <Segmented
                    size="small"
                    value={view}
                    onChange={(v) => setView(v as 'prettify' | 'raw')}
                    options={[
                      { label: 'Prettify', value: 'prettify' },
                      { label: '原文', value: 'raw' },
                    ]}
                  />
                  {view === 'raw' && (
                    <Tooltip title={wrap ? '关闭自动换行' : '开启自动换行'}>
                      <Button
                        size="small"
                        type={wrap ? 'primary' : 'text'}
                        icon={<MenuUnfoldOutlined />}
                        onClick={() => setWrap(!wrap)}
                      />
                    </Tooltip>
                  )}
                </div>
                <pre style={{
                  maxHeight: 420, overflow: 'auto', margin: 0,
                  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12,
                  color: okColor ? PALETTE.text : '#F54A45',
                  whiteSpace: view === 'raw' ? (wrap ? 'pre-wrap' : 'pre') : 'pre',
                }}>
                  {view === 'prettify'
                    ? (prettyBody ?? `非 JSON，无法美化：\n${rawBody}`)
                    : rawBody}
                </pre>
              </div>
            ),
          },
          {
            key: 'headers',
            label: '响应头',
            children: (
              <Table
                size="small" pagination={false} rowKey="k"
                columns={[
                  { title: 'Header', dataIndex: 'k', width: 260 },
                  { title: '值', dataIndex: 'v' },
                ]}
                dataSource={Object.entries(headers).map(([k, v]) => ({ k, v: String(v) }))}
              />
            ),
          },
          {
            key: 'assertions',
            label: '断言',
            children: s.assertions?.length ? (
              <Table
                size="small" pagination={false} rowKey={(_r, i) => String(i)}
                columns={[
                  { title: '断言', dataIndex: ['assertion', 'path'], render: (_v, r: any) => String(r?.assertion?.path || r?.assertion?.target || '') },
                  { title: '结果', dataIndex: 'passed', render: (v) => <Tag color={v ? 'success' : 'error'}>{v ? '通过' : '失败'}</Tag> },
                  { title: '实际', dataIndex: 'actual' },
                  { title: '消息', dataIndex: 'message' },
                ]}
                dataSource={s.assertions}
              />
            ) : <span style={{ color: PALETTE.textTertiary }}>无断言</span>,
          },
          {
            key: 'logs',
            label: '日志',
            children: s.logs?.length ? (
              <pre style={{ maxHeight: 300, overflow: 'auto', margin: 0, fontSize: 12 }}>{s.logs.join('\n')}</pre>
            ) : <span style={{ color: PALETTE.textTertiary }}>无日志</span>,
          },
        ]}
      />
    </div>
  )
}
