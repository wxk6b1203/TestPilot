import { Button, Card, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { download, get, post } from '../api'
import type { ListResp, TestRun } from '../api'
import RunDetailDrawer, { StatusTag } from '../components/RunDetailDrawer'
import { useLayout } from '../hooks/useLayout'
import { useEventStream } from '../hooks/useEventStream'
import { message } from '../messageBridge'

export default function Runs() {
  const { projectId } = useLayout()
  const [rows, setRows] = useState<TestRun[]>([])
  const [detail, setDetail] = useState<TestRun | null>(null)
  const timer = useRef<ReturnType<typeof setInterval> | undefined>(undefined)
  const detailTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const [total, setTotal] = useState(0)

  const load = async () => {
    if (!projectId) return
    // page_size 取后端上限 500：客户端分页一次拉全（后端 pageParams 硬顶 500）
    const r = await get<ListResp<TestRun>>(`/api/v1/runs?page_size=500&project_id=` + projectId)
    setRows(r.items)
    setTotal(r.total ?? 0)
  }

  // 项目切换重置 + 初始加载 + 30s 慢速兜底对账（实时更新走 SSE）
  useEffect(() => {
    setRows([])
    setDetail(null)
    load().catch((e) => message.error(e.message))
    timer.current = setInterval(() => load().catch(() => undefined), 30000)
    return () => clearInterval(timer.current)
  }, [projectId])

  // 项目 run 创建/收尾事件 → 刷新列表（step 级进度只走 run 详情通道，不刷列表）
  useEventStream(
    projectId ? [`project:${projectId}`] : [],
    (event) => {
      if (['run_created', 'run_updated', 'stress_created', 'stress_updated'].includes(event)) {
        void load().catch(() => undefined)
      }
    },
    !!projectId,
  )

  // 详情抽屉：run step_progress / run_updated → 300ms 防抖刷新详情
  useEventStream(
    detail ? [`run:${detail.id}`] : [],
    () => {
      const id = detail?.id
      if (!id) return
      if (detailTimer.current) clearTimeout(detailTimer.current)
      detailTimer.current = setTimeout(async () => {
        try {
          setDetail(await get<TestRun>(`/api/v1/runs/${id}`))
        } catch {
          /* 忽略瞬时错误 */
        }
      }, 300)
    },
    !!detail,
  )
  useEffect(() => () => {
    if (detailTimer.current) clearTimeout(detailTimer.current)
  }, [])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  return (
    <Card title="运行记录" extra={
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        共 {total} 条{total > rows.length ? `（已加载前 ${rows.length} 条）` : ''}
      </Typography.Text>
    }>
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={{ defaultPageSize: 10, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
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
