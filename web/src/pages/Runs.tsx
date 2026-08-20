import { Button, Card, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { download, get, post, warnTruncated } from '../api'
import type { ListResp, TestRun } from '../api'
import RunDetailDrawer, { StatusTag } from '../components/RunDetailDrawer'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'

export default function Runs() {
  const { projectId } = useLayout()
  const [rows, setRows] = useState<TestRun[]>([])
  const [detail, setDetail] = useState<TestRun | null>(null)
  const timer = useRef<ReturnType<typeof setInterval> | undefined>(undefined)

  const load = async () => {
    if (!projectId) return
    const r = await get<ListResp<TestRun>>(`/api/v1/runs?page_size=100&project_id=` + projectId)
    setRows(r.items)
    warnTruncated(r, '运行记录')
  }

  // 列表轮询 + 项目切换重置（不依赖 detail——此前依赖 detail 导致打开详情后
  // effect 重跑并 setDetail(null)，抽屉"弹出来又弹回去"）
  useEffect(() => {
    setRows([])
    setDetail(null)
    load().catch((e) => message.error(e.message))
    timer.current = setInterval(() => load().catch(() => undefined), 3000)
    return () => clearInterval(timer.current)
  }, [projectId])

  // 详情打开且运行中 → 独立轮询刷新详情；结束后自动停
  useEffect(() => {
    if (!detail || (detail.status !== 1 && detail.status !== 0)) return
    const t = setInterval(async () => {
      try {
        setDetail(await get<TestRun>(`/api/v1/runs/${detail.id}`))
      } catch {
        /* 忽略瞬时错误 */
      }
    }, 3000)
    return () => clearInterval(t)
  }, [detail?.id, detail?.status])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  return (
    <Card title="运行记录">
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={{ pageSize: 15 }}
        columns={[
          { title: 'ID', dataIndex: 'id', width: 190, render: (v: string) => <Typography.Text copyable={{ text: v }}>{v.slice(-8)}</Typography.Text> },
          { title: '状态', dataIndex: 'status', width: 100, render: (v: number) => <StatusTag v={v} /> },
          {
            title: '结果',
            width: 160,
            render: (_, r) =>
              r.summary ? (
                <Space size={4}>
                  <Tag>总 {r.summary.total}</Tag>
                  <Tag color="success">过 {r.summary.passed}</Tag>
                  <Tag color="error">败 {r.summary.failed}</Tag>
                </Space>
              ) : (
                '-'
              ),
          },
          { title: '开始时间', dataIndex: 'started_at', width: 170, render: (v: string) => v?.slice(0, 19).replace('T', ' ') },
          { title: '结束时间', dataIndex: 'finished_at', width: 170, render: (v?: string) => v?.slice(0, 19).replace('T', ' ') || '-' },
          {
            title: '操作',
            width: 190,
            render: (_, r) => (
              <Space>
                <Typography.Link onClick={async () => setDetail(await get(`/api/v1/runs/${r.id}`))}>详情</Typography.Link>
                <Typography.Link
                  onClick={() => {
                    download(`/api/v1/runs/${r.id}/junit`, `testpilot-run-${r.id}.xml`)
                      .catch((e) => message.error(e.message))
                  }}
                >
                  导出 JUnit
                </Typography.Link>
                {(r.status === 0 || r.status === 1) && (
                  <Popconfirm
                    title="取消该运行？未完成用例将标记为跳过"
                    onConfirm={async () => {
                      try {
                        await post(`/api/v1/runs/${r.id}/cancel`)
                        message.success('已取消')
                        void load()
                        if (detail?.id === r.id) setDetail(await get(`/api/v1/runs/${r.id}`))
                      } catch (e: any) {
                        message.error(e.message)
                      }
                    }}
                  >
                    <Button size="small" danger type="link" style={{ padding: 0 }}>取消</Button>
                  </Popconfirm>
                )}
              </Space>
            ),
          },
        ]}
      />

      <RunDetailDrawer run={detail} open={!!detail} onClose={() => setDetail(null)} />
    </Card>
  )
}
