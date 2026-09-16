import { Button, Card, Drawer, Form, Input, InputNumber, Modal, Popconfirm, Segmented, Select, Space, Table, Tag, Typography } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, HTTP_METHODS, post, STATUS } from '../api'
import type { Environment, HttpApi, ListResp, TestCase } from '../api'
import { useLayout } from '../hooks/useLayout'
import { useEventStream } from '../hooks/useEventStream'
import { message } from '../messageBridge'
import { t } from '../i18n'

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
  if (points.length < 2) return <Typography.Text type="secondary">{t('No data')}</Typography.Text>
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
        <Typography.Text type="secondary">{t('peak {v}', { v: yFmt ? yFmt(maxY) : maxY.toFixed(1) })}</Typography.Text>
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

  const [planTotal, setPlanTotal] = useState(0)
  const [runTotal, setRunTotal] = useState(0)

  // load 被 30s 定时器与 SSE 回调反复调用：内部吞错并提示，避免 unhandled rejection
  const load = () => {
    if (!projectId) return
    // page_size 取后端上限 500：客户端分页一次拉全
    get<ListResp<StressPlan>>(`/api/v1/stress-plans?project_id=${projectId}&page_size=500`)
      .then((r) => { setPlans(r.items); setPlanTotal(r.total ?? 0) })
      .catch((e) => message.error(e.message))
    get<ListResp<StressRun>>(`/api/v1/stress-runs?page_size=500&project_id=` + projectId)
      .then((r) => { setRuns(r.items); setRunTotal(r.total ?? 0) })
      .catch((e) => message.error(e.message))
  }
  useEffect(() => {
    if (!projectId) return
    load()
    get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page_size=500`)
      .then((r) => setApis(r.items)).catch((e) => message.error(e.message))
    get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=200`)
      .then((r) => setCases(r.items)).catch((e) => message.error(e.message))
    get<ListResp<Environment>>(`/api/v1/environments?project_id=${projectId}&page_size=100`)
      .then((r) => setEnvs(r.items)).catch((e) => message.error(e.message))
    const timer = setInterval(load, 30000) // 兜底对账；实时更新走 SSE
    return () => clearInterval(timer)
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

  if (!projectId) return <Card>{t('Select a project at the top first')}</Card>

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
      <Card title={t('Stress plans')} extra={
        <Space>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('{total} in total', { total: planTotal })}{planTotal > plans.length ? t(' (loaded first {count})', { count: plans.length }) : ''}
          </Typography.Text>
          <Button type="primary" onClick={() => setOpen(true)}>{t('New stress plan')}</Button>
        </Space>
      } style={{ marginBottom: 16 }}>
        <Table
          rowKey="id"
          dataSource={plans}
          pagination={{ defaultPageSize: 10, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
          columns={[
            {
              title: t('Type'), dataIndex: 'target_type', width: 90,
              render: (v: number) => <Tag color={v === 2 ? 'purple' : 'blue'}>{v === 2 ? t('Behavior case') : t('API')}</Tag>,
            },
            { title: t('Target'), dataIndex: 'target_id', render: (v: string, r: StressPlan) => (r.target_type === 2 ? caseName(v) : apiName(v)) },
            { title: t('Workers'), dataIndex: 'worker_count', width: 90 },
            {
              title: t('Load'), dataIndex: 'load_profile', render: (v: any) => {
                const ramp = v?.ramp?.map((s: any) => `${s.at}→${s.target}`).join(' / ') || '-'
                return <Typography.Text code style={{ fontSize: 12 }}>{ramp} · {v?.duration}</Typography.Text>
              },
            },
            {
              title: t('Actions'), width: 200,
              render: (_, r) => (
                <Space>
                  <Button size="small" type="primary" onClick={async () => {
                    try {
                      const res = await post(`/api/v1/stress-plans/${r.id}/run`, {})
                      message.success(t('Stress run triggered: {id}', { id: res.run_id }))
                      load()
                    } catch (e: any) {
                      message.error(e.message)
                    }
                  }}>{t('Start stress')}</Button>
                  <Popconfirm title={t('Delete this plan?')} onConfirm={async () => {
                    try {
                      await del(`/api/v1/stress-plans/${r.id}`)
                      load()
                    } catch (e: any) {
                      message.error(e.message)
                    }
                  }}>
                    <Button danger size="small">{t('Delete')}</Button>
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      </Card>

      <Card title={t('Stress runs')} extra={
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('{total} in total', { total: runTotal })}{runTotal > runs.length ? t(' (loaded first {count})', { count: runs.length }) : ''}
        </Typography.Text>
      }>
        <Table
          rowKey="id"
          dataSource={runs}
          pagination={{ defaultPageSize: 10, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
          columns={[
            { title: 'ID', dataIndex: 'id', width: 190, render: (v: string) => <Typography.Text copyable={{ text: v }}>{v.slice(-8)}</Typography.Text> },
            { title: t('Status'), dataIndex: 'status', width: 100, render: (v: number) => <Tag color={(STATUS[v]?.color as string) || 'default'}>{STATUS[v]?.text || v}</Tag> },
            {
              title: t('Summary'), dataIndex: 'summary',
              render: (v: any) => v ? (
                <Space size={4} wrap>
                  <Tag>{t('avg {v}', { v: `${Number(v.avg_rps ?? 0).toFixed(0)} rps` })}</Tag>
                  <Tag>{t('p95 peak {v}', { v: `${Number(v.max_p95_ms ?? 0).toFixed(0)}ms` })}</Tag>
                  <Tag color={Number(v.avg_error_rate) > 0 ? 'error' : 'success'}>{t('error rate {v}', { v: `${(Number(v.avg_error_rate ?? 0) * 100).toFixed(2)}%` })}</Tag>
                </Space>
              ) : '-',
            },
            { title: t('Started at'), dataIndex: 'started_at', width: 170, render: (v: string) => v?.slice(0, 19).replace('T', ' ') },
            {
              title: t('Actions'), width: 90,
              render: (_, r) => (
                <Typography.Link onClick={async () => {
                  try {
                    setDetail(await get<StressRun>(`/api/v1/stress-runs/${r.id}`))
                  } catch (e: any) {
                    message.error(e.message)
                  }
                }}>{t('Report')}</Typography.Link>
              ),
            },
          ]}
        />
      </Card>

      <Modal title={t('New stress plan')} open={open} width={640} onCancel={() => setOpen(false)} onOk={() => form.submit()} destroyOnHidden>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12 }}>
          {t('Stress a single API or a low-code behavior case (Locust library mode, dedicated subprocess + gevent). ramp is stepped ramp-up: at = start time, target = total concurrency.')}
        </Typography.Paragraph>
        <Form form={form} layout="vertical" onFinish={async (v) => {
          let profile: any
          try {
            profile = JSON.parse(v.load_profile)
          } catch (e: any) {
            message.error(t('Invalid load_profile JSON: {msg}', { msg: e.message }))
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
            message.success(t('Created'))
          } catch (e: any) {
            message.error(e.message)
          }
        }}>
          <Form.Item name="target_type" label={t('Target type')} initialValue={1}>
            <Segmented
              options={[{ label: t('API'), value: 1 }, { label: t('Behavior case'), value: 2 }]}
              onChange={() => form.setFieldValue('target_id', undefined)}
            />
          </Form.Item>
          <Form.Item name="target_id" label={t('Target')} rules={[{ required: true, message: t('Select a target') }]}>
            <Select
              showSearch optionFilterProp="label"
              placeholder={targetType === 2 ? t('Select a low-code case') : t('Select an API')}
              options={targetType === 2
                ? lowCodeCases.map((c) => ({ value: c.id, label: `[${t('case')}] ${c.name}` }))
                : apis.map((a) => ({ value: a.id, label: `[${HTTP_METHODS[a.method]?.text || a.method}] ${a.uri}` }))}
            />
          </Form.Item>
          <Form.Item name="env_id" label={t('Environment')} rules={[{ required: true }]}>
            <Select options={envs.map((e) => ({ value: e.id, label: `${e.name} (${e.base_url})` }))} />
          </Form.Item>
          <Form.Item name="load_profile" label="LoadProfile（JSON）" initialValue={PROFILE_EXAMPLE} rules={[{ required: true }]}>
            <Input.TextArea rows={9} style={{ fontFamily: 'monospace', fontSize: 12 }} />
          </Form.Item>
          <Space size={16}>
            <Form.Item name="worker_count" label={t('Stress worker count')} initialValue={1}>
              <InputNumber min={1} max={16} />
            </Form.Item>
            <Form.Item name="metrics_interval_ms" label={t('Metrics interval (ms)')} initialValue={1000}>
              <InputNumber min={200} max={10000} step={100} />
            </Form.Item>
          </Space>
        </Form>
      </Modal>

      <Drawer
        title={detail ? t('Stress report {id}', { id: detail.id.slice(-8) }) : ''}
        open={!!detail}
        onClose={() => setDetail(null)}
        width={860}
      >
        {detail && (
          <Space orientation="vertical" style={{ width: '100%' }} size={16}>
            {detail.summary && (
              <Space size={8} wrap>
                <Tag>{t('samples {n}', { n: detail.summary.samples })}</Tag>
                <Tag>{t('avg {v}', { v: `${Number(detail.summary.avg_rps ?? 0).toFixed(1)} rps` })}</Tag>
                <Tag>{t('peak {v}', { v: `${Number(detail.summary.peak_rps ?? 0).toFixed(1)} rps` })}</Tag>
                <Tag>{t('p95 peak {v}', { v: `${Number(detail.summary.max_p95_ms ?? 0).toFixed(1)} ms` })}</Tag>
                <Tag>{t('max concurrency {n}', { n: detail.summary.max_concurrency })}</Tag>
                <Tag color={Number(detail.summary.avg_error_rate) > 0.01 ? 'error' : 'success'}>
                  {t('error rate {v}', { v: `${(Number(detail.summary.avg_error_rate ?? 0) * 100).toFixed(2)}%` })}
                </Tag>
              </Space>
            )}
            <div>
              <Typography.Title level={5}>{t('RPS / concurrency')}</Typography.Title>
              <SeriesChart points={detail.metrics || []} series={[
                { key: 'rps', label: 'RPS', color: '#1677ff' },
                { key: 'concurrency', label: t('Concurrency'), color: '#722ed1' },
              ]} yFmt={(v) => v.toFixed(0)} />
            </div>
            <div>
              <Typography.Title level={5}>{t('Latency (ms)')}</Typography.Title>
              <SeriesChart points={detail.metrics || []} series={[
                { key: 'latency_p50_ms', label: 'p50', color: '#52c41a' },
                { key: 'latency_p95_ms', label: 'p95', color: '#fa8c16' },
                { key: 'latency_p99_ms', label: 'p99', color: '#f5222d' },
              ]} yFmt={(v) => `${v.toFixed(1)}ms`} />
            </div>
            <div>
              <Typography.Title level={5}>{t('Error rate')}</Typography.Title>
              <SeriesChart points={detail.metrics || []} series={[
                { key: 'error_rate', label: t('Error rate'), color: '#f5222d' },
              ]} yFmt={(v) => `${(v * 100).toFixed(1)}%`} />
            </div>
          </Space>
        )}
      </Drawer>
    </>
  )
}
