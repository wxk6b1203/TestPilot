import { Button, Card, Col, Form, Input, Modal, Popconfirm, Row, Select, Space, Switch, Table } from 'antd'
import { useCallback, useEffect, useState } from 'react'
import { del, get, post, put } from '../api'
import type { Environment, ListResp, Variable } from '../api'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'
import { t } from '../i18n'

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

  if (!projectId) return <Card>{t('Select a project at the top first')}</Card>

  return (
    <Row gutter={16}>
      <Col span={10}>
        <Card title={t('Environments')} extra={<Button type="primary" size="small" onClick={() => setEnvOpen(true)}>{t('New')}</Button>}>
          <Table
            rowKey="id"
            size="small"
            dataSource={envs}
            pagination={false}
            columns={[
              { title: t('Name'), dataIndex: 'name' },
              { title: 'Base URL', dataIndex: 'base_url' },
              {
                title: t('Actions'),
                width: 150,
                render: (_, r) => (
                  <Space size={4}>
                    <Button size="small" onClick={() => openEnvEdit(r)}>{t('Edit')}</Button>
                    <Popconfirm title={t('Delete this environment?')} onConfirm={async () => {
                      try {
                        await del(`/api/v1/environments/${r.id}`)
                        await Promise.all([loadEnvs(), refreshEnvs()])
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
      </Col>
      <Col span={14}>
        <Card title={t('Variables')} extra={<Button type="primary" size="small" onClick={() => setVarOpen(true)}>{t('New')}</Button>}>
          <Table
            rowKey="id"
            size="small"
            dataSource={vars}
            pagination={{ defaultPageSize: 10, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
            columns={[
              { title: 'Key', dataIndex: 'key' },
              { title: 'Value', dataIndex: 'value', render: (v: string, r) => (r.sensitive ? '••••••' : v) },
              {
                title: t('Environment'),
                dataIndex: 'environment_id',
                render: (v: string) => (v === '0' || !v ? t('Project-level') : envs.find((e) => e.id === v)?.name || v),
              },
              {
                title: t('Actions'),
                width: 150,
                render: (_, r) => (
                  <Space size={4}>
                    <Button size="small" onClick={() => openVarEdit(r)}>{t('Edit')}</Button>
                    <Popconfirm title={t('Delete this variable?')} onConfirm={async () => {
                      try {
                        await del(`/api/v1/variables/${r.id}`)
                        loadVars()
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
      </Col>

      <Modal
        title={editingEnv ? t('Edit environment') : t('New environment')}
        open={envOpen}
        onCancel={() => { setEnvOpen(false); setEditingEnv(null) }}
        onOk={() => envForm.submit()}
        destroyOnHidden
      >
        <Form form={envForm} layout="vertical" onFinish={async (v) => {
          try {
            if (editingEnv) {
              await put(`/api/v1/environments/${editingEnv.id}`, v)
              message.success(t('Saved'))
            } else {
              await post('/api/v1/environments', { ...v, project_id: projectId })
              message.success(t('Created'))
            }
            setEnvOpen(false)
            setEditingEnv(null)
            envForm.resetFields()
            await Promise.all([loadEnvs(), refreshEnvs()])
          } catch (e: any) {
            message.error(e.message)
          }
        }}>
          <Form.Item name="name" label={t('Name')} rules={[{ required: true }]}>
            <Input placeholder="local / staging / prod" />
          </Form.Item>
          <Form.Item name="base_url" label="Base URL" rules={[{ required: true }]}>
            <Input placeholder="http://127.0.0.1:18080" />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title={editingVar ? t('Edit variable') : t('New variable')}
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
                message.success(t('Saved'))
              } else {
                await post('/api/v1/variables', { ...v, project_id: projectId })
                message.success(t('Created'))
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
          <Form.Item name="environment_id" label={t('Scoped environment')}>
            <Select
              options={[
                { value: '0', label: t('Project-level (all environments)') },
                ...envs.map((e) => ({ value: e.id, label: e.name })),
              ]}
            />
          </Form.Item>
          <Form.Item name="sensitive" label={t('Sensitive (secret_ref; never sent in plain text)')} valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </Row>
  )
}
