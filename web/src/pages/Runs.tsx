import { Badge, Button, Card, Collapse, Descriptions, Drawer, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { get, getToken, post, STATUS, warnTruncated } from '../api'
import type { Artifact, ListResp, TestRun } from '../api'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'

function StatusTag({ v }: { v: number }) {
  const s = STATUS[v] || { text: String(v), color: 'default' }
  return <Badge status={s.color as any} text={s.text} />
}

const ART_KIND: Record<number, string> = { 1: '截图', 2: '视频', 3: 'Trace', 4: 'HAR', 5: '下载', 6: '日志' }

// 产物经 blob 拉取（接口需 Bearer，<img>/<a> 直连不带头）
function useArtifactUrl(id: string) {
  const [url, setUrl] = useState<string>()
  useEffect(() => {
    let obj: string | undefined
    let dead = false
    fetch(`/api/v1/artifacts/${id}/content`, { headers: { Authorization: `Bearer ${getToken()}` } })
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((b) => {
        if (dead) return
        obj = URL.createObjectURL(b)
        setUrl(obj)
      })
      .catch(() => undefined)
    return () => {
      dead = true
      if (obj) URL.revokeObjectURL(obj)
    }
  }, [id])
  return url
}

function ArtifactView({ a }: { a: Artifact }) {
  const url = useArtifactUrl(a.id)
  const name = a.uri.split('/').pop() || `artifact-${a.id}`
  if (!url) return <Tag>{ART_KIND[a.kind] || a.kind} 加载中…</Tag>
  if (a.kind === 1) {
    return (
      <a href={url} target="_blank" rel="noreferrer">
        <img src={url} alt={name} style={{ maxWidth: '100%', maxHeight: 320, border: '1px solid #444' }} />
      </a>
    )
  }
  const hint = a.kind === 3 ? '（可用 npx playwright show-trace 回放）' : ''
  return (
    <Typography.Link href={url} download={name}>
      {ART_KIND[a.kind] || '产物'}: {name}（{(a.size / 1024).toFixed(1)}KB）{hint}
    </Typography.Link>
  )
}

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
            width: 150,
            render: (_, r) => (
              <Space>
                <Typography.Link onClick={async () => setDetail(await get(`/api/v1/runs/${r.id}`))}>详情</Typography.Link>
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

      <Drawer
        title={detail ? `运行 ${detail.id.slice(-8)} — ${STATUS[detail.status]?.text}` : ''}
        open={!!detail}
        onClose={() => setDetail(null)}
        width={860}
      >
        {detail && (
          <>
            {detail.summary?.error && (
              <Typography.Paragraph type="danger">{detail.summary.error}</Typography.Paragraph>
            )}
            <Collapse
              defaultActiveKey={detail.cases?.map((c) => c.id)}
              items={(detail.cases || []).map((c) => ({
                key: c.id,
                label: (
                  <Space>
                    <StatusTag v={c.status} />
                    <b>{c.case_name || c.case_id}</b>
                    <Typography.Text type="secondary">{c.duration_ms}ms</Typography.Text>
                    {c.error && <Typography.Text type="danger">{c.error}</Typography.Text>}
                  </Space>
                ),
                children: (
                  <Collapse
                    ghost
                    items={c.steps.map((s) => ({
                      key: s.step_path,
                      label: (
                        <Space>
                          <StatusTag v={s.status} />
                          <Typography.Text code>{s.step_path}</Typography.Text>
                          <Typography.Text type="secondary">{s.duration_ms}ms</Typography.Text>
                        </Space>
                      ),
                      children: (
                        <div style={{ fontSize: 12 }}>
                          {(s.logs || []).map((l, i) => (
                            <div key={i}><Typography.Text type="secondary">log: {l}</Typography.Text></div>
                          ))}
                          {(s.artifacts || []).map((a) => (
                            <div key={a.id} style={{ margin: '6px 0' }}><ArtifactView a={a} /></div>
                          ))}
                          {(s.assertions || []).map((a, i) => (
                            <div key={i}>
                              <Tag color={a.passed ? 'success' : 'error'}>{a.passed ? 'PASS' : 'FAIL'}</Tag>
                              <Typography.Text>
                                target={a.assertion?.target} path={a.assertion?.path || '-'} op={a.assertion?.op}，
                                实际 {a.actual || '-'}，期望 {a.assertion?.expected || '-'}（{a.message}）
                              </Typography.Text>
                            </div>
                          ))}
                          {s.request && (
                            <Descriptions size="small" column={1} style={{ marginTop: 8 }}
                              items={[
                                { key: 'q', label: '请求', children: <pre style={{ margin: 0 }}>{JSON.stringify(s.request, null, 2)}</pre> },
                                { key: 'p', label: '响应', children: <pre style={{ margin: 0, maxHeight: 240, overflow: 'auto' }}>{JSON.stringify(s.response, null, 2)}</pre> },
                              ]}
                            />
                          )}
                        </div>
                      ),
                    }))}
                  />
                ),
              }))}
            />
          </>
        )}
      </Drawer>
    </Card>
  )
}
