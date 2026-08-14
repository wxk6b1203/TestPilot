import { Button, Card, Input, InputNumber, Modal, Segmented, Select, Space, Spin, Switch, Tag, Typography, message } from 'antd'
import {
  ArrowDownOutlined, ArrowLeftOutlined, ArrowUpOutlined, DeleteOutlined,
  PlayCircleOutlined, PlusOutlined, SaveOutlined, SettingOutlined,
} from '@ant-design/icons'
import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, put } from '../api'
import type { ListResp, PlanItem, Suite, TestCase, TestPlan } from '../api'
import IdeLayout from '../components/IdeLayout'
import { PALETTE, SPACING } from '../theme'
import { useLayout } from './Layout'

// api.ts 的 PlanItem 尚无 param_overrides 字段（后端 planPayload 支持），此处本地扩展。
interface PlanItemX extends PlanItem {
  param_overrides?: any
}
type PlanDetail = Omit<TestPlan, 'items'> & { items?: PlanItemX[] }

interface EditableItem {
  ref_type: number // 1=用例 2=套件
  ref_id: string
  enabled: boolean
  param_overrides?: any
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div style={{ fontSize: 12, color: PALETTE.textSecondary, marginBottom: 4 }}>{label}</div>
      {children}
    </div>
  )
}

// 测试计划编辑器：基本信息 + 执行条目编排（用例/套件引用、排序、参数覆盖）。
export default function PlanEditor() {
  const { id } = useParams()
  const nav = useNavigate()
  const { projectId, envs } = useLayout()
  const [loading, setLoading] = useState(true)
  const [name, setName] = useState('')
  const [envId, setEnvId] = useState('')
  const [concurrency, setConcurrency] = useState(1)
  const [timeoutMs, setTimeoutMs] = useState(300000)
  const [items, setItems] = useState<EditableItem[]>([])
  const [cases, setCases] = useState<TestCase[]>([])
  const [suites, setSuites] = useState<Suite[]>([])
  const [saving, setSaving] = useState(false)
  const [running, setRunning] = useState(false)
  const [ovIndex, setOvIndex] = useState<number | null>(null)
  const [ovText, setOvText] = useState('')

  // 计划详情回填（items 后端已按 order 升序返回）
  useEffect(() => {
    if (!id) return
    setLoading(true)
    get<PlanDetail>(`/api/v1/plans/${id}`)
      .then((p) => {
        setName(p.name)
        setEnvId(p.env_id)
        setConcurrency(p.concurrency)
        setTimeoutMs(p.timeout_ms)
        setItems(
          (p.items ?? []).map((it) => ({
            ref_type: it.ref_type,
            ref_id: it.ref_id,
            enabled: it.enabled,
            param_overrides: it.param_overrides,
          })),
        )
      })
      .catch((e) => message.error(e.message))
      .finally(() => setLoading(false))
  }, [id])

  // 用例/套件选项（随项目刷新）
  useEffect(() => {
    if (!projectId) return
    get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=200`)
      .then((r) => setCases(r.items))
      .catch((e) => message.error(e.message))
    get<ListResp<Suite>>(`/api/v1/suites?project_id=${projectId}`)
      .then((r) => setSuites(r.items))
      .catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  const update = (i: number, patch: Partial<EditableItem>) =>
    setItems((prev) => prev.map((it, j) => (j === i ? { ...it, ...patch } : it)))
  const remove = (i: number) => setItems((prev) => prev.filter((_, j) => j !== i))
  const move = (i: number, delta: number) =>
    setItems((prev) => {
      const j = i + delta
      if (j < 0 || j >= prev.length) return prev
      const next = [...prev]
      const t = next[i]
      next[i] = next[j]
      next[j] = t
      return next
    })
  const add = () => setItems((prev) => [...prev, { ref_type: 1, ref_id: '', enabled: true }])

  const save = async () => {
    if (!name.trim()) { message.error('请填写计划名称'); return }
    if (!envId) { message.error('请选择环境'); return }
    if (items.length === 0) { message.error('请至少添加一个执行条目'); return }
    if (items.some((it) => !it.ref_id)) { message.error('存在未选择用例/套件的条目'); return }
    setSaving(true)
    try {
      await put(`/api/v1/plans/${id}`, {
        project_id: projectId,
        env_id: envId,
        name: name.trim(),
        concurrency,
        timeout_ms: timeoutMs,
        // 后端全量替换 items；order 按数组下标重建
        items: items.map((it, i) => ({
          ref_type: it.ref_type,
          ref_id: it.ref_id,
          enabled: it.enabled,
          order: i,
          param_overrides: it.param_overrides,
        })),
      })
      message.success('已保存')
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  const run = async () => {
    setRunning(true)
    try {
      const r = await post<{ run_id: string }>(`/api/v1/plans/${id}/run`, {})
      message.success(`已触发运行 ${r.run_id}`)
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setRunning(false)
    }
  }

  const openOverride = (i: number) => {
    const it = items[i]
    setOvIndex(i)
    setOvText(it?.param_overrides ? JSON.stringify(it.param_overrides, null, 2) : '')
  }

  const applyOverride = () => {
    if (ovIndex === null) return
    const text = ovText.trim()
    if (text === '') {
      // 留空 = 删除参数覆盖字段
      update(ovIndex, { param_overrides: undefined })
      setOvIndex(null)
      return
    }
    let parsed: any
    try {
      parsed = JSON.parse(text)
    } catch (e: any) {
      message.error(`不是合法 JSON：${e.message}`)
      return
    }
    update(ovIndex, { param_overrides: parsed })
    setOvIndex(null)
  }

  return (
    <IdeLayout
      toolbar={
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Space>
            <Button size="small" icon={<ArrowLeftOutlined />} onClick={() => nav('/plans')}>
              返回
            </Button>
            <span style={{ fontWeight: 600, fontSize: 14, color: PALETTE.text }}>编辑测试计划</span>
          </Space>
          <Space>
            <Button size="small" icon={<PlayCircleOutlined />} loading={running} onClick={run}>
              运行
            </Button>
            <Button size="small" type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>
              保存
            </Button>
          </Space>
        </div>
      }
    >
      <Spin spinning={loading}>
        <div style={{ padding: SPACING[4], maxWidth: 1080 }}>
          {/* 基本信息 */}
          <div
            style={{
              display: 'flex', flexWrap: 'wrap', alignItems: 'flex-end', gap: SPACING[3],
              paddingBottom: SPACING[4], borderBottom: `1px solid ${PALETTE.border}`,
            }}
          >
            <Field label="名称">
              <Input
                style={{ width: 280 }}
                value={name}
                placeholder="计划名称"
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field label="环境">
              <Select
                style={{ width: 220 }}
                placeholder="选择环境"
                value={envId || undefined}
                options={envs.map((e) => ({ value: e.id, label: `${e.name} (${e.base_url})` }))}
                onChange={(v) => setEnvId(v ? String(v) : '')}
              />
            </Field>
            <Field label="并发">
              <InputNumber
                style={{ width: 90 }}
                min={1}
                max={32}
                value={concurrency}
                onChange={(v) => setConcurrency(v ?? 1)}
              />
            </Field>
            <Field label="超时（ms）">
              <InputNumber
                style={{ width: 130 }}
                min={1000}
                step={60000}
                value={timeoutMs}
                onChange={(v) => setTimeoutMs(v ?? 300000)}
              />
            </Field>
          </div>

          {/* 执行条目 */}
          <div style={{ paddingTop: SPACING[4] }}>
            <div
              style={{
                display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                marginBottom: SPACING[2],
              }}
            >
              <Typography.Text strong>执行条目（{items.length}）</Typography.Text>
              <Button size="small" icon={<PlusOutlined />} onClick={add}>
                添加条目
              </Button>
            </div>
            {items.length === 0 && (
              <div
                style={{
                  textAlign: 'center', color: PALETTE.textTertiary, padding: 24,
                  border: `1px dashed ${PALETTE.border}`, borderRadius: 6,
                }}
              >
                暂无条目，点击「添加条目」开始编排
              </div>
            )}
            {items.map((it, i) => (
              <div
                key={i}
                style={{
                  display: 'flex', alignItems: 'center', gap: SPACING[2], padding: '6px 0',
                  borderBottom: i === items.length - 1 ? 'none' : `1px solid ${PALETTE.border}`,
                }}
              >
                <span style={{ width: 20, textAlign: 'center', color: PALETTE.textTertiary, fontSize: 12 }}>
                  {i + 1}
                </span>
                <Segmented
                  size="small"
                  value={it.ref_type}
                  options={[
                    { label: '用例', value: 1 },
                    { label: '套件', value: 2 },
                  ]}
                  onChange={(v) => update(i, { ref_type: Number(v), ref_id: '' })}
                />
                {it.ref_type === 1 ? (
                  <Select
                    showSearch
                    style={{ flex: 1, minWidth: 220 }}
                    placeholder="选择用例"
                    value={it.ref_id || undefined}
                    optionFilterProp="label"
                    options={cases.map((c) => ({ value: c.id, label: c.name }))}
                    optionRender={(opt) => {
                      const c = cases.find((x) => x.id === String(opt.value))
                      if (!c) return opt.label
                      return (
                        <span style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                          <span>{c.name}</span>
                          <Tag style={{ margin: 0, fontSize: 11 }}>
                            {c.type === 1 ? '声明式' : '低代码'}
                          </Tag>
                        </span>
                      )
                    }}
                    onChange={(v) => update(i, { ref_id: v ? String(v) : '' })}
                  />
                ) : (
                  <Select
                    showSearch
                    style={{ flex: 1, minWidth: 220 }}
                    placeholder="选择套件"
                    value={it.ref_id || undefined}
                    optionFilterProp="label"
                    options={suites.map((s) => ({ value: s.id, label: s.name }))}
                    onChange={(v) => update(i, { ref_id: v ? String(v) : '' })}
                  />
                )}
                <Space size={6}>
                  <span style={{ fontSize: 12, color: PALETTE.textSecondary }}>启用</span>
                  <Switch size="small" checked={it.enabled} onChange={(v) => update(i, { enabled: v })} />
                </Space>
                <Button
                  size="small"
                  type="dashed"
                  icon={<SettingOutlined />}
                  onClick={() => openOverride(i)}
                >
                  参数覆盖
                </Button>
                <Button
                  size="small"
                  type="text"
                  icon={<ArrowUpOutlined />}
                  disabled={i === 0}
                  onClick={() => move(i, -1)}
                />
                <Button
                  size="small"
                  type="text"
                  icon={<ArrowDownOutlined />}
                  disabled={i === items.length - 1}
                  onClick={() => move(i, 1)}
                />
                <Button
                  size="small"
                  type="text"
                  danger
                  icon={<DeleteOutlined />}
                  onClick={() => remove(i)}
                />
              </div>
            ))}
          </div>
        </div>
      </Spin>

      {/* 参数覆盖编辑 */}
      <Modal
        title={ovIndex !== null ? `参数覆盖 · 第 ${ovIndex + 1} 项` : '参数覆盖'}
        open={ovIndex !== null}
        width={640}
        okText="确定"
        onCancel={() => setOvIndex(null)}
        onOk={applyOverride}
        destroyOnHidden
      >
        <Input.TextArea
          rows={12}
          value={ovText}
          placeholder='{"key": "value"}'
          style={{ fontFamily: 'monospace', fontSize: 12 }}
          onChange={(e) => setOvText(e.target.value)}
        />
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 8, marginBottom: 0 }}>
          留空并确定将清除该项的参数覆盖；否则请输入合法 JSON 对象。
        </Typography.Paragraph>
      </Modal>
    </IdeLayout>
  )
}
