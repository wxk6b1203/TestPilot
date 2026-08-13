import { Button, Card, Form, Input, Modal, Popconfirm, Table, message } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, post } from '../api'
import type { ListResp, Project } from '../api'
import { useLayout } from './Layout'

export default function Projects() {
  const { refreshProjects } = useLayout()
  const [rows, setRows] = useState<Project[]>([])
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm()

  const load = () =>
    get<ListResp<Project>>('/api/v1/projects?page_size=100').then((r) => setRows(r.items))
  useEffect(() => {
    load().catch((e) => message.error(e.message))
  }, [])

  return (
    <Card
      title="项目"
      extra={
        <Button type="primary" onClick={() => setOpen(true)}>新建项目</Button>
      }
    >
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={false}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '描述', dataIndex: 'description' },
          { title: '创建时间', dataIndex: 'created_at', render: (v: string) => v?.slice(0, 19).replace('T', ' ') },
          {
            title: '操作',
            render: (_, r) => (
              <Popconfirm
                title="删除项目？"
                onConfirm={async () => {
                  await del(`/api/v1/projects/${r.id}`)
                  await load()
                  await refreshProjects()
                  message.success('已删除')
                }}
              >
                <Button danger size="small">删除</Button>
              </Popconfirm>
            ),
          },
        ]}
      />
      <Modal
        title="新建项目"
        open={open}
        onCancel={() => setOpen(false)}
        onOk={() => form.submit()}
        destroyOnHidden
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={async (v) => {
            await post('/api/v1/projects', v)
            setOpen(false)
            form.resetFields()
            await load()
            await refreshProjects()
            message.success('已创建')
          }}
        >
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input.TextArea rows={2} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
