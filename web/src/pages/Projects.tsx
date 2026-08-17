import { Button, Card, Form, Input, Modal, Popconfirm, Table } from 'antd'
import { useEffect, useState } from 'react'
import { del, get, post } from '../api'
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
  const [form] = Form.useForm()

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
        <Button type="primary" onClick={() => setOpen(true)}>新建项目</Button>
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
          { title: '名称', dataIndex: 'name' },
          { title: '描述', dataIndex: 'description' },
          { title: '创建时间', dataIndex: 'created_at', render: (v: string) => v?.slice(0, 19).replace('T', ' ') },
          {
            title: '操作',
            render: (_, r) => (
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
            try {
              await post('/api/v1/projects', v)
              setOpen(false)
              form.resetFields()
              await load(1, pageSize)
              await refreshProjects()
              message.success('已创建')
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
