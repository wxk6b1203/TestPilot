import { Button, Card, Form, Input, Modal, Popconfirm, Select, Space, Table, Tag } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useCallback, useEffect, useState } from 'react'
import { del, get, post, put } from '../api'
import type { Certificate, ListResp } from '../api'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'

// 证书管理页：当前为资产 CRUD（pem/p12 引用）。
// cert_ref/key_ref 为统一凭证引用；Worker 实际加载客户端证书依赖密钥后端，暂属另议项。

export default function Certificates() {
  const { projectId } = useLayout()
  const [items, setItems] = useState<Certificate[]>([])
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Certificate | null>(null)
  const [total, setTotal] = useState(0)
  const [form] = Form.useForm()

  const load = useCallback(() => {
    if (!projectId) return Promise.resolve()
    setLoading(true)
    return get<ListResp<Certificate>>(`/api/v1/certificates?project_id=${projectId}&page_size=500`)
      .then((r) => {
        setItems(r.items)
        setTotal(r.total ?? 0)
      })
      .catch((e) => message.error(e.message))
      .finally(() => setLoading(false))
  }, [projectId])

  useEffect(() => {
    setItems([])
    void load()
  }, [load])

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    form.setFieldsValue({ type: 'pem' })
    setOpen(true)
  }

  const openEdit = (c: Certificate) => {
    setEditing(c)
    form.resetFields()
    form.setFieldsValue(c)
    setOpen(true)
  }

  const submit = async () => {
    const values = await form.validateFields()
    if (editing) {
      await put(`/api/v1/certificates/${editing.id}`, { ...values, project_id: projectId })
      message.success('已保存')
    } else {
      await post('/api/v1/certificates', { ...values, project_id: projectId })
      message.success('已创建')
    }
    setOpen(false)
    void load()
  }

  const remove = async (id: string) => {
    await del(`/api/v1/certificates/${id}`)
    message.success('已删除')
    void load()
  }

  return (
    <Card
      title={`证书（共 ${total} 张）`}
      extra={(
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>新建证书</Button>
      )}
      style={{ margin: 16 }}
    >
      <Table<Certificate>
        rowKey="id"
        loading={loading}
        dataSource={items}
        pagination={{ defaultPageSize: 10, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
        columns={[
          { title: '名称', dataIndex: 'name' },
          {
            title: '类型', dataIndex: 'type', width: 100,
            render: (v: string) => <Tag color={v === 'p12' ? 'orange' : 'blue'}>{v || 'pem'}</Tag>,
          },
          { title: '证书引用', dataIndex: 'cert_ref', ellipsis: true },
          { title: '密钥引用', dataIndex: 'key_ref', ellipsis: true },
          { title: '描述', dataIndex: 'description', ellipsis: true },
          {
            title: '操作', width: 140,
            render: (_, c) => (
              <Space>
                <a onClick={() => openEdit(c)}>编辑</a>
                <Popconfirm title="删除该证书？" onConfirm={() => remove(c.id)}>
                  <a style={{ color: '#ff4d4f' }}>删除</a>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={editing ? '编辑证书' : '新建证书'}
        open={open}
        onCancel={() => setOpen(false)}
        onOk={submit}
        destroyOnHidden
        okText="保存"
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入名称' }]}>
            <Input placeholder="例如：内部网关客户端证书" />
          </Form.Item>
          <Form.Item name="type" label="类型" rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'pem', label: 'PEM' },
                { value: 'p12', label: 'P12 / PKCS#12' },
              ]}
            />
          </Form.Item>
          <Form.Item name="cert_ref" label="证书引用 cert_ref">
            <Input placeholder="artifact://... 或密钥后端引用" />
          </Form.Item>
          <Form.Item name="key_ref" label="私钥引用 key_ref">
            <Input placeholder="artifact://... 或密钥后端引用" />
          </Form.Item>
          <Form.Item name="password_secret_ref" label="口令引用 password_secret_ref">
            <Input placeholder="vault://... / secret_ref" />
          </Form.Item>
          <Form.Item name="description" label="描述">
            <Input.TextArea rows={3} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
