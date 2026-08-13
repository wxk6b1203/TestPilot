import { Button, Card, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Table, message } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, post } from '../api'
import type { Environment, ListResp, TestCase, TestPlan } from '../api'
import { useLayout } from './Layout'

export default function Plans() {
  const { projectId } = useLayout()
  const [rows, setRows] = useState<TestPlan[]>([])
  const [envs, setEnvs] = useState<Environment[]>([])
  const [cases, setCases] = useState<TestCase[]>([])
  const [open, setOpen] = useState(false)
  const [running, setRunning] = useState<string>('')
  const [form] = Form.useForm()

  const load = () => {
    if (!projectId) return Promise.resolve()
    get<ListResp<TestPlan>>(`/api/v1/plans?project_id=${projectId}&page_size=500`).then((r) => setRows(r.items))
    get<ListResp<Environment>>(`/api/v1/environments?project_id=${projectId}`).then((r) => setEnvs(r.items))
    get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=500`).then((r) => setCases(r.items))
    return Promise.resolve()
  }
  useEffect(() => {
    setRows([])
    load().catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  const runPlan = async (id: string) => {
    setRunning(id)
    try {
      const r = await post<{ run_id: string }>(`/api/v1/plans/${id}/run`, {})
      message.success(`已触发运行 ${r.run_id}`)
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setRunning('')
    }
  }

  return (
    <Card title="测试计划" extra={<Button type="primary" onClick={() => setOpen(true)}>新建计划</Button>}>
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={{ pageSize: 15 }}
        columns={[
          { title: '名称', dataIndex: 'name' },
          {
            title: '环境',
            dataIndex: 'env_id',
            width: 120,
            render: (v: string) => envs.find((e) => e.id === v)?.name || v || '-',
          },
          {
            title: '操作',
            width: 200,
            render: (_, r) => (
              <Space>
                <Button size="small" type="primary" loading={running === r.id} onClick={() => runPlan(r.id)}>
                  运行
                </Button>
                <Popconfirm title="删除计划？" onConfirm={async () => {
                  await del(`/api/v1/plans/${r.id}`)
                  load()
                }}>
                  <Button danger size="small">删除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />

      <Modal title="新建计划" open={open} width={560} onCancel={() => setOpen(false)} onOk={() => form.submit()} destroyOnHidden>
        <Form form={form} layout="vertical" initialValues={{ concurrency: 1, timeout_ms: 300000 }}
          onFinish={async (v) => {
            const items = (v.case_ids || []).map((cid: string, i: number) => ({
              ref_type: 1, ref_id: cid, enabled: true, order: i + 1,
            }))
            if (items.length === 0) {
              message.error('至少选择一个用例')
              return
            }
            await post('/api/v1/plans', {
              project_id: projectId, name: v.name, env_id: v.env_id,
              concurrency: v.concurrency, timeout_ms: v.timeout_ms, items,
            })
            setOpen(false)
            form.resetFields()
            load()
            message.success('已创建')
          }}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="env_id" label="环境" rules={[{ required: true }]}>
            <Select options={envs.map((e) => ({ value: e.id, label: `${e.name} (${e.base_url})` }))} />
          </Form.Item>
          <Form.Item name="case_ids" label="用例（按选择顺序执行）" rules={[{ required: true }]}>
            <Select mode="multiple" options={cases.map((c) => ({ value: c.id, label: c.name }))} />
          </Form.Item>
          <Space size={16}>
            <Form.Item name="concurrency" label="并发">
              <InputNumber min={1} max={32} />
            </Form.Item>
            <Form.Item name="timeout_ms" label="超时（ms）">
              <InputNumber min={1000} step={60000} />
            </Form.Item>
          </Space>
        </Form>
      </Modal>
    </Card>
  )
}
