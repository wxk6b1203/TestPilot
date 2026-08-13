import { Button, Card, Form, Input, Modal, Popconfirm, Select, Space, Table, Tag, message } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, HTTP_METHODS, post } from '../api'
import type { HttpApi, ListResp } from '../api'
import { useLayout } from './Layout'

export default function Apis() {
  const { projectId } = useLayout()
  const [rows, setRows] = useState<HttpApi[]>([])
  const [open, setOpen] = useState(false)
  const [curlOpen, setCurlOpen] = useState(false)
  const [oasOpen, setOasOpen] = useState(false)
  const [form] = Form.useForm()
  const [curlCmd, setCurlCmd] = useState('')
  const [oasDoc, setOasDoc] = useState('')

  const load = () =>
    projectId
      ? get<ListResp<HttpApi>>(`/api/v1/apis?project_id=${projectId}&page_size=500`).then((r) => setRows(r.items))
      : Promise.resolve()
  useEffect(() => {
    setRows([])
    load().catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  return (
    <Card
      title="接口"
      extra={
        <Space>
          <Button onClick={() => setCurlOpen(true)}>导入 curl</Button>
          <Button onClick={() => setOasOpen(true)}>导入 OpenAPI</Button>
          <Button href={`/api/v1/export/openapi?project_id=${projectId}`} target="_blank">导出 OpenAPI</Button>
          <Button type="primary" onClick={() => setOpen(true)}>新建接口</Button>
        </Space>
      }
    >
      <Table
        rowKey="id"
        size="middle"
        dataSource={rows}
        pagination={{ pageSize: 15 }}
        columns={[
          {
            title: '方法',
            dataIndex: 'method',
            width: 90,
            render: (v: number) => <Tag color={HTTP_METHODS[v]?.color}>{HTTP_METHODS[v]?.text || v}</Tag>,
          },
          { title: 'URI', dataIndex: 'uri' },
          {
            title: 'Headers',
            width: 80,
            render: (_, r) => r.headers?.length || 0,
          },
          {
            title: 'Body',
            width: 80,
            render: (_, r) => (r.body?.raw ? `${r.body.raw.length}B` : '-'),
          },
          {
            title: '操作',
            width: 80,
            render: (_, r) => (
              <Popconfirm title="删除接口？" onConfirm={async () => {
                await del(`/api/v1/apis/${r.id}`)
                load()
              }}>
                <Button danger size="small">删除</Button>
              </Popconfirm>
            ),
          },
        ]}
      />

      <Modal title="新建接口" open={open} onCancel={() => setOpen(false)} onOk={() => form.submit()} destroyOnHidden width={560}>
        <Form form={form} layout="vertical" initialValues={{ method: 1 }}
          onFinish={async (v) => {
            const headers = (v.headers || '').split('\n').filter(Boolean).map((line: string) => {
              const [key, ...rest] = line.split(':')
              return { key: key.trim(), value: rest.join(':').trim() }
            })
            const body = v.body
              ? { contentType: 4, raw: v.body }
              : undefined
            await post('/api/v1/apis', { ...v, project_id: projectId, headers, body })
            setOpen(false)
            form.resetFields()
            load()
            message.success('已创建')
          }}>
          <Space.Compact block>
            <Form.Item name="method" style={{ width: 120 }}>
              <Select options={Object.entries(HTTP_METHODS).map(([v, m]) => ({ value: Number(v), label: m.text }))} />
            </Form.Item>
            <Form.Item name="uri" style={{ flex: 1 }} rules={[{ required: true, message: 'URI 必填' }]}>
              <Input placeholder="/users/{id} 或完整 URL" />
            </Form.Item>
          </Space.Compact>
          <Form.Item name="headers" label="Headers（每行一个 Key: Value）">
            <Input.TextArea rows={3} placeholder={'Content-Type: application/json\nX-Tenant: {{tenant}}'} />
          </Form.Item>
          <Form.Item name="body" label="Body（JSON 原文，可含 {{var}}）">
            <Input.TextArea rows={4} placeholder='{"name": "neo"}' />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        title="导入 curl"
        open={curlOpen}
        onCancel={() => setCurlOpen(false)}
        onOk={async () => {
          try {
            await post('/api/v1/import/curl', { project_id: projectId, command: curlCmd })
            setCurlOpen(false)
            setCurlCmd('')
            load()
            message.success('已导入')
          } catch (e: any) {
            message.error(e.message)
          }
        }}
        okText="导入"
      >
        <Input.TextArea
          rows={6}
          value={curlCmd}
          onChange={(e) => setCurlCmd(e.target.value)}
          placeholder={"curl -X POST 'http://api.example.com/users' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"name\": \"neo\"}'"}
        />
      </Modal>

      <Modal
        title="导入 OpenAPI（JSON / YAML）"
        open={oasOpen}
        onCancel={() => setOasOpen(false)}
        onOk={async () => {
          try {
            let document: any
            try {
              document = JSON.parse(oasDoc)
            } catch {
              document = undefined
            }
            const r = await post<{ created: number; skipped: number }>('/api/v1/import/openapi',
              document ? { project_id: projectId, document } : { project_id: projectId, document_yaml: oasDoc })
            setOasOpen(false)
            setOasDoc('')
            load()
            message.success(`导入 ${r.created} 个，跳过 ${r.skipped} 个`)
          } catch (e: any) {
            message.error(e.message)
          }
        }}
        okText="导入"
        width={640}
      >
        <Input.TextArea
          rows={12}
          value={oasDoc}
          onChange={(e) => setOasDoc(e.target.value)}
          placeholder='{"openapi": "3.0.0", "paths": {...}}'
        />
      </Modal>
    </Card>
  )
}
