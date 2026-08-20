import { Button, Card, Drawer, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Table, Tag, Typography } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, HTTP_METHODS, post, STATUS, warnTruncated } from '../api'
import type { Environment, HttpApi, ListResp, TestCase } from '../api'
import { useLayout } from '../hooks/useLayout'
import { useEventStream } from '../hooks/useEventStream'
import { message } from '../messageBridge'

interface StressPlan {
  id: string
  project_id: string
  env_id: string
  target_type: number
  target_id: string
  load_profile: any
  worker_count: number
  metrics_interval_ms: number
}
interface MetricPoint {
  ts: string
  rps: number
  latency_p50_ms: number
  latency_p95_ms: number
  latency_p99_ms: number
  error_rate: number
  concurrency: number
}
interface StressRun {
  id: string
  stress_plan_id: string
  status: number
  summary?: any
  started_at: string
  finished_at?: string
  metrics?: MetricPoint[]
}

const PROFILE_EXAMPLE = `{
  "ramp": [
    { "at": "0s",  "target": 2 },
    { "at": "5s",  "target": 10 },
    { "at": "10s", "target": 20 }
  ],
  "duration": "20s",
  "concurrency_per_worker": 20
}`

// 轻量 SVG 时序图（无第三方图表依赖）
function SeriesChart({ points, series, height = 160, yFmt }: {
  points: MetricPoint[]
  series: { key: keyof MetricPoint; label: string; color: string }[]
  height?: number
  yFmt?: (v: number) => string
}) {
  const W = 760, H = height, PAD = 8
  if (points.length < 2) return <Typography.Text type="secondary">暂无数据</Typography.Text>
  const maxY = Math.max(0.0001, ...points.flatMap((p) => series.map((s) => Number(p[s.key]) || 0)))
  const x = (i: number) => PAD + (i / (points.length - 1)) * (W - 2 * PAD)
  const y = (v: number) => H - PAD - (v / maxY) * (H - 2 * PAD)
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', background: 'rgba(128,128,128,0.06)', borderRadius: 4 }}>
        {series.map((s) => (
          <polyline
            key={String(s.key)}
            fill="none"
            stroke={s.color}
            strokeWidth={1.6}
            points={points.map((p, i) => `${x(i)},${y(Number(p[s.key]) || 0)}`).join(' ')}
          />
        ))}
      </svg>
      <Space size={12} style={{ fontSize: 12 }}>
        {series.map((s) => (
          <span key={String(s.key)}><span style={{ color: s.color }}>●</span> {s.label}</span>
        ))}
        <Typography.Text type="secondary">峰值 {yFmt ? yFmt(maxY) : maxY.toFixed(1)}</Typography.Text>
      </Space>
    </div>
  )
}

export default function Stress() {
  const { projectId } = useLayout()
  const [plans, setPlans] = useState<StressPlan[]>([])
  const [runs, setRuns] = useState<StressRun[]>([])
  const [apis, setApis] = useState<HttpApi[]>([])
  const [cases, setCases] = useState<TestCase[]>([])
  const [envs, setEnvs] = useState<Environment[]>([])
  const [open, setOpen] = useState(false)
  const [detail, setDetail] = useState<StressRun | null>(null)
  const [form] = Form.useForm()
  const targetType = Form.useWatch('target_type', form) ?? 1

  const load = () => {
    if (!projectId) return
    get<ListResp<StressPlan>>(`/api/v1/stress-plans?project_id=${projectId}&page_size=200`)
      .then((r) => { setPlans(r.items); warnTruncated(r, '压测计划') })
    get<ListResp<StressRun>>(`/api/v1/stress-runs?page_size=50&project_id=` + projectId)
      .then((r) => { setRuns(r.items); warnTruncated(r, '压测运行') })
  }
  useEffect(() => {
    if (!projectId) return
    load()
    get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page_size=500`).then((r) => setApis(r.items))
    get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=200`).then((r) => setCases(r.items))
    get<ListResp<Environment>>(`/api/v1/environments?project_id=${projectId}&page_size=100`).then((r) => setEnvs(r.items))
    const t = setInterval(load, 30000) // 兜底对账；实时更新走 SSE
    return () => clearInterval(t)
  }, [projectId])

  // 项目压测创建/收尾事件 → 刷新列表
  useEventStream(
    projectId ? [`project:${projectId}`] : [],
    (event) => {
      if (['stress_created', 'stress_updated'].includes(event)) load()
    },
    !!projectId,
  )

  // 报告抽屉：指标点直接追加，收尾事件拉全量详情
  useEventStream(
    detail ? [`stress:${detail.id}`] : [],
    (event, data) => {
      if (!detail) return
      if (event === 'stress_metrics' && Array.isArray(data?.points)) {
        const pts = data.points as MetricPoint[]
        setDetail((prev) => prev ? {
          ...prev,
          metrics: [...(prev.metrics || []), ...pts].slice(-3000),
        } : prev)
        return
      }
      if (event === 'stress_updated') {
        get<StressRun>(`/api/v1/stress-runs/${detail.id}`).then(setDetail).catch(() => {})
      }
    },
    !!detail,
  )

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  const apiName = (id: string) => {
    const a = apis.find((x) => x.id === id)
    return a ? `[${HTTP_METHODS[a.method]?.text || a.method}] ${a.uri}` : id.slice(-8)
  }
  const caseName = (id: string) => {
    const c = cases.find((x) => x.id === id)
    return c ? c.name : id.slice(-8)
  }
  const lowCodeCases = cases.filter((c) => c.type === 2)

  return (
    <>
      <Card title="压测计划" extra={<Button type="primary" onClick={() => setOpen(true)}>新建压测计划</Button>} style={{ marginBottom: 16 }}>
        <Table
          rowKey="id"
          dataSource={plans}
          pagination={{ pageSize: 10 }}
          columns={[
            {
              title: '类型', dataIndex: 'target_type', width: 90,
              render: (v: number) => <Tag color={v === 2 ? 'purple' : 'blue'}>{v === 2 ? '行为用例' : '接口'}</Tag>,
            },
            { title: '目标', dataIndex: 'target_id', render: (v: string, r: StressPlan) => (r.target_type === 2 ? caseName(v) : apiName(v)) },
            { title: 'Worker 数', dataIndex: 'worker_count', width: 90 },
            {
              title: '负载', dataIndex: 'load_profile', render: (v: any) => {
                const ramp = v?.ramp?.map((s: any) => `${s.at}→${s.target}`).join(' / ') || '-'
                return <Typography.Text code style={{ fontSize: 12 }}>{ramp} · {v?.duration}</Typography.Text>
              },
            },
            {
              title: '操作', width: 200,
              render: (_, r) => (
                <Space>
                  <Button size="small" type="primary" onClick={async () => {
                    const res = await post(`/api/v1/stress-plans/${r.id}/run`, {})
                    message.success(`压测已触发: ${res.run_id}`)
                    load()
                  }}>发压</Button>
                  <Popconfirm title="删除计划？" onConfirm={async () => { await del(`/api/v1/stress-plans/${r.id}`); load() }}>
                    <Button danger size="small">删除</Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <Card title="压测运行">
        <Table
          rowKey="id"
          dataSource={runs}
          pagination={{ pageSize: 10 }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 190, render: (v: string) => <Typography.Text copyable={{ text: v }}>{v.slice(-8)}</Typography.Text> },
            { title: '状态', dataIndex: 'status', width: 100, render: (v: number) => <Tag color={(STATUS[v]?.color as string) || 'default'}>{STATUS[v]?.text || v}</Tag> },
            {
              title: '摘要', dataIndex: 'summary',
              render: (v: any) => v ? (
                <Space size={4} wrap>
                  <Tag>均值 {Number(v.avg_rps ?? 0).toFixed(0)} rps</Tag>
                  <Tag>p95峰值 {Number(v.max_p95_ms ?? 0).toFixed(0)}ms</Tag>
                  <Tag color={Number(v.avg_error_rate) > 0 ? 'error' : 'success'}>错误率 {(Number(v.avg_error_rate ?? 0) * 100).toFixed(2)}%</Tag>
                </Space>
              ) : '-',
            },
            { title: '开始时间', dataIndex: 'started_at', width: 170, render: (v: string) => v?.slice(0, 19).replace('T', ' ') },
            {
              title: '操作', width: 90,
              render: (_, r) => (
                <Typography.Link onClick={async () => setDetail(await get(`/api/v1/stress-runs/${r.id}`))}>报告</Typography.Link>
              ),
            },
          ]}
        />
      </Card>

      <Modal title="新建压测计划" open={open} width={640} onCancel={() => setOpen(false)} onOk={() => form.submit()} destroyOnHidden>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          对单个接口或低代码行为用例发压（Locust 库模式，独立子进程 + gevent）。ramp 为阶梯加压（at=起点，target=总并发）。
        </Typography.Paragraph>
        <Form form={form} layout="vertical" onFinish={async (v) => {
          let profile: any
          try {
            profile = JSON.parse(v.load_profile)
          } catch (e: any) {
            message.error(`load_profile 不是合法 JSON：${e.message}`)
            return
          }
          try {
            await post('/api/v1/stress-plans', {
              project_id: projectId, env_id: v.env_id, target_type: v.target_type ?? 1, target_id: v.target_id,
              load_profile: profile, worker_count: v.worker_count ?? 1,
              metrics_interval_ms: v.metrics_interval_ms ?? 1000,
            })
            setOpen(false)
            form.resetFields()
            load()
            message.success('已创建')
          } catch (e: any) {
            message.error(e.message)
          }
        }}>
          <Form.Item name="target_type" label="目标类型" initialValue={1}>
            <Segmented
              options={[{ label: '接口', value: 1 }, { label: '行为用例', value: 2 }]}
              onChange={() => form.setFieldValue('target_id', undefined)}
            />
          </Form.Item>
          <Form.Item name="target_id" label="目标" rules={[{ required: true, message: '请选择目标' }]}>
            <Select
              showSearch optionFilterProp="label"
              placeholder={targetType === 2 ? '选择低代码用例' : '选择接口'}
              options={targetType === 2
                ? lowCodeCases.map((c) => ({ value: c.id, label: `[用例] ${c.name}` }))
                : apis.map((a) => ({ value: a.id, label: `[${HTTP_METHODS[a.method]?.text || a.method}] ${a.uri}` }))}
            />
          </Form.Item>
          <Form.Item name="env_id" label="环境" rules={[{ required: true }]}>
            <Select options={envs.map((e) => ({ value: e.id, label: `${e.name} (${e.base_url})` }))} />
          </Form.Item>
          <Form.Item name="load_profile" label="LoadProfile（JSON）" initialValue={PROFILE_EXAMPLE} rules={[{ required: true }]}>
            <Input.TextArea rows={9} style={{ fontFamily: 'monospace', fontSize: 12 }} />
          </Form.Item>
          <Space size={16}>
            <Form.Item name="worker_count" label="发压 Worker 数" initialValue={1}>
              <InputNumber min={1} max={16} />
            </Form.Item>
            <Form.Item name="metrics_interval_ms" label="采样间隔 ms" initialValue={1000}>
              <InputNumber min={200} max={10000} step={100} />
            </Form.Item>
          </Space>
        </Form>
      </Modal>

      <Drawer
        title={detail ? `压测报告 ${detail.id.slice(-8)}` : ''}
        open={!!detail}
        onClose={() => setDetail(null)}
        width={860}
      >
        {detail && (
          <Space orientation="vertical" style={{ width: '100%' }} size={16}>
            {detail.summary && (
              <Space size={8} wrap>
                <Tag>样本 {detail.summary.samples}</Tag>
                <Tag>均值 {Number(detail.summary.avg_rps ?? 0).toFixed(1)} rps</Tag>
                <Tag>峰值 {Number(detail.summary.peak_rps ?? 0).toFixed(1)} rps</Tag>
                <Tag>p95峰值 {Number(detail.summary.max_p95_ms ?? 0).toFixed(1)} ms</Tag>
                <Tag>最大并发 {detail.summary.max_concurrency}</Tag>
                <Tag color={Number(detail.summary.avg_error_rate) > 0.01 ? 'error' : 'success'}>
                  错误率 {(Number(detail.summary.avg_error_rate ?? 0) * 100).toFixed(2)}%
                </Tag>
              </Space>
            )}
            <div>
              <Typography.Title level={5}>RPS / 并发</Typography.Title>
              <SeriesChart points={detail.metrics || []} series={[
                { key: 'rps', label: 'RPS', color: '#1677ff' },
                { key: 'concurrency', label: '并发', color: '#722ed1' },
              ]} yFmt={(v) => v.toFixed(0)} />
            </div>
            <div>
              <Typography.Title level={5}>延迟（ms）</Typography.Title>
              <SeriesChart points={detail.metrics || []} series={[
                { key: 'latency_p50_ms', label: 'p50', color: '#52c41a' },
                { key: 'latency_p95_ms', label: 'p95', color: '#fa8c16' },
                { key: 'latency_p99_ms', label: 'p99', color: '#f5222d' },
              ]} yFmt={(v) => `${v.toFixed(1)}ms`} />
            </div>
            <div>
              <Typography.Title level={5}>错误率</Typography.Title>
              <SeriesChart points={detail.metrics || []} series={[
                { key: 'error_rate', label: '错误率', color: '#f5222d' },
              ]} yFmt={(v) => `${(v * 100).toFixed(1)}%`} />
            </div>
          </Space>
        )}
      </Drawer>
    </>
  )
}
