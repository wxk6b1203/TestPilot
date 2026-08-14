import { useState } from 'react'
import { Empty, Table, Tabs, Tag } from 'antd'
import { RocketOutlined } from '@ant-design/icons'
import { PALETTE } from '../theme'
import type { DebugResult } from '../api'

// 响应面板：状态/耗时 + 响应体（JSON 高亮）/响应头/断言/日志。
export default function ResponsePane({ result, loading }: { result?: DebugResult; loading?: boolean }) {
  const [tab, setTab] = useState('body')
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
  const bodyText = (() => {
    const b = resp.body
    if (typeof b === 'string') return b
    return JSON.stringify(b ?? resp.json ?? null, null, 2)
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
            children: (
              <pre style={{
                maxHeight: 420, overflow: 'auto', margin: 0,
                fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12,
                color: okColor ? PALETTE.text : '#F54A45',
              }}>
                {bodyText}
              </pre>
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
