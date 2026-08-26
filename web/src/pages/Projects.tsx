import { Button, Card, Form, Input, Modal, Popconfirm, Space, Table, Typography } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, post, put } from '../api'
import type { ListResp, Project } from '../api'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'

export default function Projects() {
  const { refreshProjects } = useLayout()
  const [rows, setRows] = useState<Project[]>([])
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [total, setTotal] = useState(0)
  const [open, setOpen] = useState(false)
  // 非空表示弹框处于编辑模式（复用新建弹框，同 Environments 页约定）
  const [editing, setEditing] = useState<Project | null>(null)
  const [form] = Form.useForm()

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    setOpen(true)
  }

  const openEdit = (r: Project) => {
    setEditing(r)
    form.setFieldsValue({ name: r.name, description: r.description })
    setOpen(true)
  }

  const load = async (p = page, ps = pageSize) => {
    const r = await get<ListResp<Project>>(`/api/v1/projects?page=${p}&page_size=${ps}`)
    setRows(r.items)
    setTotal(r.total)
    setPage(p)
    setPageSize(ps)
  }

  useEffect(() => {
    load(1, 10).catch((e) => message.error(e.message))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const reloadAfterChange = async (deleted = false) => {
    // 删除当前页最后一条时回退一页，避免停留在空页
    const next = deleted && rows.length === 1 && page > 1 ? page - 1 : page
    await load(next, pageSize)
  }

  return (
    <Card
      title="项目"
      extra={
        <Button type="primary" onClick={openCreate}>新建项目</Button>
      }
    >
      <Table
        rowKey="id"
        dataSource={rows}
        pagination={{
          current: page,
          pageSize,
          total,
          size: 'small',
          showSizeChanger: true,
          showTotal: (t) => `共 ${t} 条`,
          onChange: (p, ps) => { void load(p, ps) },
        }}
        columns={[
          {
            title: 'ID', dataIndex: 'id', width: 190,
            render: (v: string) => <Typography.Text copyable={{ text: v }}>{v.slice(-8)}</Typography.Text>,
          },
          { title: '名称', dataIndex: 'name' },
          { title: '描述', dataIndex: 'description' },
          { title: '创建时间', dataIndex: 'created_at', render: (v: string) => v?.slice(0, 19).replace('T', ' ') },
          {
            title: '操作',
            width: 150,
            render: (_, r) => (
              <Space size={4}>
                <Button size="small" onClick={() => openEdit(r)}>编辑</Button>
                <Popconfirm
                  title="删除项目？"
                  onConfirm={async () => {
                    try {
                      await del(`/api/v1/projects/${r.id}`)
                      await reloadAfterChange(true)
                      await refreshProjects()
                      message.success('已删除')
                    } catch (e: any) {
                      message.error(e.message)
                    }
                  }}
                >
                  <Button danger size="small">删除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={editing ? '编辑项目' : '新建项目'}
        open={open}
        onCancel={() => { setOpen(false); setEditing(null) }}
        onOk={() => form.submit()}
        destroyOnHidden
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={async (v) => {
            try {
              // 编辑留在当前页；新建跳回第一页展示新条目
              if (editing) {
                await put(`/api/v1/projects/${editing.id}`, v)
              } else {
                await post('/api/v1/projects', v)
              }
              const p = editing ? page : 1
              setOpen(false)
              setEditing(null)
              form.resetFields()
              await load(p, pageSize)
              await refreshProjects()
              message.success(editing ? '已保存' : '已创建')
            } catch (e: any) {
              message.error(e.message)
            }
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
