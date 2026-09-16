import { Card, Table, Tag } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { CAPS, get } from '../api'
import type { ListResp, WorkerInfo } from '../api'
import { useEventStream } from '../hooks/useEventStream'
import { message } from '../messageBridge'
import { t } from '../i18n'

export default function Workers() {
  const [rows, setRows] = useState<WorkerInfo[]>([])
  const timer = useRef<ReturnType<typeof setInterval> | undefined>(undefined)

  const load = () =>
    get<ListResp<WorkerInfo>>('/api/v1/workers').then((r) => setRows(r.items))

  useEffect(() => {
    load().catch((e) => message.error(e.message))
    timer.current = setInterval(() => load().catch(() => undefined), 30000) // 兜底对账
    return () => clearInterval(timer.current)
  }, [])

  useEventStream(['workers'], () => void load().catch(() => undefined))

  return (
    <Card title={t('Online workers')}>
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={false}
        columns={[
          { title: 'ID', dataIndex: 'id' },
          {
            title: t('Capabilities'),
            dataIndex: 'capabilities',
            render: (v: number[]) => v.map((c) => <Tag key={c} color="blue">{CAPS[c] || c}</Tag>),
          },
          {
            title: t('Load'),
            width: 120,
            render: (_, r) => `${r.load} / ${r.max_concurrency}`,
          },
          {
            title: t('Tags'),
            dataIndex: 'tags',
            render: (v: string[]) => (v || []).map((t) => <Tag key={t}>{t}</Tag>),
          },
          { title: 'SDK', dataIndex: 'sdk_version', width: 80 },
          {
            title: t('Tenant'),
            dataIndex: 'tenant_id',
            width: 100,
            render: (v: string) => (v === '0' ? t('Shared') : v),
          },
        ]}
      />
    </Card>
  )
}
