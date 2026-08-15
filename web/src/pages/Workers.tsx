import { Card, Table, Tag } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { CAPS, get } from '../api'
import type { ListResp, WorkerInfo } from '../api'
import { message } from '../messageBridge'

export default function Workers() {
  const [rows, setRows] = useState<WorkerInfo[]>([])
  const timer = useRef<ReturnType<typeof setInterval> | undefined>(undefined)

  const load = () =>
    get<ListResp<WorkerInfo>>('/api/v1/workers').then((r) => setRows(r.items))

  useEffect(() => {
    load().catch((e) => message.error(e.message))
    timer.current = setInterval(() => load().catch(() => undefined), 5000)
    return () => clearInterval(timer.current)
  }, [])

  return (
    <Card title="在线 Worker">
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={false}
        columns={[
          { title: 'ID', dataIndex: 'id' },
          {
            title: '能力',
            dataIndex: 'capabilities',
            render: (v: number[]) => v.map((c) => <Tag key={c} color="blue">{CAPS[c] || c}</Tag>),
          },
          {
            title: '负载',
            width: 120,
            render: (_, r) => `${r.load} / ${r.max_concurrency}`,
          },
          {
            title: '标签',
            dataIndex: 'tags',
            render: (v: string[]) => (v || []).map((t) => <Tag key={t}>{t}</Tag>),
          },
          { title: 'SDK', dataIndex: 'sdk_version', width: 80 },
          {
            title: '租户',
            dataIndex: 'tenant_id',
            width: 100,
            render: (v: string) => (v === '0' ? '共享' : v),
          },
        ]}
      />
    </Card>
  )
}
