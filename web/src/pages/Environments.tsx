import { Button, Card, Col, Form, Input, Modal, Popconfirm, Row, Select, Space, Switch, Table } from 'antd'
import { useCallback, useEffect, useState } from 'react'
import { del, get, post, put } from '../api'
import type { Environment, ListResp, Variable } from '../api'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'

export default function Environments() {
  const { projectId, refreshEnvs } = useLayout()
  const [envs, setEnvs] = useState<Environment[]>([])
  const [vars, setVars] = useState<Variable[]>([])
  const [envOpen, setEnvOpen] = useState(false)
  const [varOpen, setVarOpen] = useState(false)
  const [editingEnv, setEditingEnv] = useState<Environment | null>(null)
  const [editingVar, setEditingVar] = useState<Variable | null>(null)
  const [envForm] = Form.useForm()
  const [varForm] = Form.useForm()

  const loadEnvs = useCallback(
    () =>
      projectId
        ? get<ListResp<Environment>>(`/api/v1/environments?project_id=${projectId}`).then((r) => setEnvs(r.items))
        : Promise.resolve(),
    [projectId],
  )
  const loadVars = useCallback(
    () =>
      projectId
        ? get<ListResp<Variable>>(`/api/v1/variables?project_id=${projectId}&page_size=500`).then((r) => { setVars(r.items) })
        : Promise.resolve(),
    [projectId],
  )

  const openEnvEdit = (r: Environment) => {
    setEditingEnv(r)
    envForm.setFieldsValue({ name: r.name, base_url: r.base_url })
    setEnvOpen(true)
  }

  const openVarEdit = (r: Variable) => {
    setEditingVar(r)
    varForm.setFieldsValue({
      key: r.key,
      value: r.value,
      environment_id: r.environment_id === '0' ? '0' : r.environment_id,
      sensitive: r.sensitive,
    })
    setVarOpen(true)
  }


  useEffect(() => {
    setEnvs([])
    setVars([])
    loadEnvs().catch((e) => message.error(e.message))
    loadVars().catch((e) => message.error(e.message))
  }, [projectId, loadEnvs, loadVars])

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
                width: 150,
                render: (_, r) => (
                  <Space size={4}>
                    <Button size="small" onClick={() => openEnvEdit(r)}>编辑</Button>
                    <Popconfirm title="删除环境？" onConfirm={async () => {
                      await del(`/api/v1/environments/${r.id}`)
                      await Promise.all([loadEnvs(), refreshEnvs()])
                    }}>
                      <Button danger size="small">删除</Button>
                    </Popconfirm>
                  </Space>
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
            pagination={{ defaultPageSize: 10, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
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
                width: 150,
                render: (_, r) => (
                  <Space size={4}>
                    <Button size="small" onClick={() => openVarEdit(r)}>编辑</Button>
                    <Popconfirm title="删除变量？" onConfirm={async () => {
                      await del(`/api/v1/variables/${r.id}`)
                      loadVars()
                    }}>
                      <Button danger size="small">删除</Button>
                    </Popconfirm>
                  </Space>
                ),
              },
            ]}
          />
        </Card>
      </Col>

      <Modal
        title={editingEnv ? '编辑环境' : '新建环境'}
        open={envOpen}
        onCancel={() => { setEnvOpen(false); setEditingEnv(null) }}
        onOk={() => envForm.submit()}
        destroyOnHidden
      >
        <Form form={envForm} layout="vertical" onFinish={async (v) => {
          try {
            if (editingEnv) {
              await put(`/api/v1/environments/${editingEnv.id}`, v)
              message.success('已保存')
            } else {
              await post('/api/v1/environments', { ...v, project_id: projectId })
              message.success('已创建')
            }
            setEnvOpen(false)
            setEditingEnv(null)
            envForm.resetFields()
            await Promise.all([loadEnvs(), refreshEnvs()])
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

      <Modal
        title={editingVar ? '编辑变量' : '新建变量'}
        open={varOpen}
        onCancel={() => { setVarOpen(false); setEditingVar(null) }}
        onOk={() => varForm.submit()}
        destroyOnHidden
      >
        <Form form={varForm} layout="vertical" initialValues={{ scope: 1, category: 1 }}
          onFinish={async (v) => {
            try {
              if (editingVar) {
                await put(`/api/v1/variables/${editingVar.id}`, v)
                message.success('已保存')
              } else {
                await post('/api/v1/variables', { ...v, project_id: projectId })
                message.success('已创建')
              }
              setVarOpen(false)
              setEditingVar(null)
              varForm.resetFields()
              loadVars()
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
