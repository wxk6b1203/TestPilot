import { Button, Card, Col, Form, Input, Modal, Popconfirm, Row, Select, Switch, Table } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, post, warnTruncated } from '../api'
import type { Environment, ListResp, Variable } from '../api'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'

export default function Environments() {
  const { projectId } = useLayout()
  const [envs, setEnvs] = useState<Environment[]>([])
  const [vars, setVars] = useState<Variable[]>([])
  const [envOpen, setEnvOpen] = useState(false)
  const [varOpen, setVarOpen] = useState(false)
  const [envForm] = Form.useForm()
  const [varForm] = Form.useForm()

  const loadEnvs = () =>
    projectId
      ? get<ListResp<Environment>>(`/api/v1/environments?project_id=${projectId}`).then((r) => setEnvs(r.items))
      : Promise.resolve()
  const loadVars = () =>
    projectId
      ? get<ListResp<Variable>>(`/api/v1/variables?project_id=${projectId}&page_size=500`).then((r) => { setVars(r.items); warnTruncated(r, '变量') })
      : Promise.resolve()

  useEffect(() => {
    setEnvs([])
    setVars([])
    loadEnvs().catch((e) => message.error(e.message))
    loadVars().catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  return (
    <Row gutter={16}>
      <Col span={10}>
        <Card title="环境" extra={<Button type="primary" size="small" onClick={() => setEnvOpen(true)}>新建</Button>}>
          <Table
            rowKey="id"
            size="small"
            dataSource={envs}
            pagination={false}
            columns={[
              { title: '名称', dataIndex: 'name' },
              { title: 'Base URL', dataIndex: 'base_url' },
              {
                title: '操作',
                width: 80,
                render: (_, r) => (
                  <Popconfirm title="删除环境？" onConfirm={async () => {
                    await del(`/api/v1/environments/${r.id}`)
                    loadEnvs()
                  }}>
                    <Button danger size="small">删除</Button>
                  </Popconfirm>
                ),
              },
            ]}
          />
        </Card>
      </Col>
      <Col span={14}>
        <Card title="变量" extra={<Button type="primary" size="small" onClick={() => setVarOpen(true)}>新建</Button>}>
          <Table
            rowKey="id"
            size="small"
            dataSource={vars}
            pagination={{ pageSize: 12 }}
            columns={[
              { title: 'Key', dataIndex: 'key' },
              { title: 'Value', dataIndex: 'value', render: (v: string, r) => (r.sensitive ? '••••••' : v) },
              {
                title: '环境',
                dataIndex: 'environment_id',
                render: (v: string) => (v === '0' || !v ? '项目级' : envs.find((e) => e.id === v)?.name || v),
              },
              {
                title: '操作',
                width: 80,
                render: (_, r) => (
                  <Popconfirm title="删除变量？" onConfirm={async () => {
                    await del(`/api/v1/variables/${r.id}`)
                    loadVars()
                  }}>
                    <Button danger size="small">删除</Button>
                  </Popconfirm>
                ),
              },
            ]}
          />
        </Card>
      </Col>

      <Modal title="新建环境" open={envOpen} onCancel={() => setEnvOpen(false)} onOk={() => envForm.submit()} destroyOnHidden>
        <Form form={envForm} layout="vertical" onFinish={async (v) => {
          try {
            await post('/api/v1/environments', { ...v, project_id: projectId })
            setEnvOpen(false)
            envForm.resetFields()
            loadEnvs()
            message.success('已创建')
          } catch (e: any) {
            message.error(e.message)
          }
        }}>
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input placeholder="local / staging / prod" />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true }]}>
            <Input placeholder="http://127.0.0.1:18080" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal title="新建变量" open={varOpen} onCancel={() => setVarOpen(false)} onOk={() => varForm.submit()} destroyOnHidden>
        <Form form={varForm} layout="vertical" initialValues={{ scope: 1, category: 1 }}
          onFinish={async (v) => {
            try {
              await post('/api/v1/variables', { ...v, project_id: projectId })
              setVarOpen(false)
              varForm.resetFields()
              loadVars()
              message.success('已创建')
            } catch (e: any) {
              message.error(e.message)
            }
          }}>
          <Form.Item name="key" label="Key" rules={[{ required: true }]}>
            <Input placeholder="token" />
          </Form.Item>
          <Form.Item name="value" label="Value" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="environment_id" label="作用环境">
            <Select
              options={[
                { value: '0', label: '项目级（所有环境）' },
                ...envs.map((e) => ({ value: e.id, label: e.name })),
              ]}
            />
          </Form.Item>
          <Form.Item name="sensitive" label="敏感（secret_ref，不明文下发）" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Row>
  )
}
