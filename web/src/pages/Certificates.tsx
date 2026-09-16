import { Button, Card, Form, Input, Modal, Popconfirm, Select, Space, Table, Tag } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useCallback, useEffect, useState } from 'react'
import { del, get, post, put } from '../api'
import type { Certificate, ListResp } from '../api'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'
import { t } from '../i18n'

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

  // 校验失败留在 antd 字段提示（与 AdminConsole 一致）；接口失败必须 message.error，
  // 否则 onOk/onConfirm 直接引用本函数时 rejection 无人接、用户零反馈。
  const submit = async () => {
    const values = await form.validateFields()
    try {
      if (editing) {
        await put(`/api/v1/certificates/${editing.id}`, { ...values, project_id: projectId })
        message.success(t('Saved'))
      } else {
        await post('/api/v1/certificates', { ...values, project_id: projectId })
        message.success(t('Created'))
      }
      setOpen(false)
      void load()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const remove = async (id: string) => {
    try {
      await del(`/api/v1/certificates/${id}`)
      message.success(t('Deleted'))
      void load()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  return (
    <Card
      title={t('Certificates ({total})', { total })}
      extra={(
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>{t('New certificate')}</Button>
      )}
      style={{ margin: 16 }}
    >
      <Table<Certificate>
        rowKey="id"
        loading={loading}
        dataSource={items}
        pagination={{ defaultPageSize: 10, showSizeChanger: true, pageSizeOptions: [10, 20, 50, 100] }}
        columns={[
          { title: t('Name'), dataIndex: 'name' },
          {
            title: t('Type'), dataIndex: 'type', width: 100,
            render: (v: string) => <Tag color={v === 'p12' ? 'orange' : 'blue'}>{v || 'pem'}</Tag>,
          },
          { title: t('Cert reference'), dataIndex: 'cert_ref', ellipsis: true },
          { title: t('Key reference'), dataIndex: 'key_ref', ellipsis: true },
          { title: t('Description'), dataIndex: 'description', ellipsis: true },
          {
            title: t('Actions'), width: 140,
            render: (_, c) => (
              <Space>
                <a onClick={() => openEdit(c)}>{t('Edit')}</a>
                <Popconfirm title={t('Delete this certificate?')} onConfirm={() => remove(c.id)}>
                  <a style={{ color: '#ff4d4f' }}>{t('Delete')}</a>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={editing ? t('Edit certificate') : t('New certificate')}
        open={open}
        onCancel={() => setOpen(false)}
        onOk={submit}
        destroyOnHidden
        okText={t('Save')}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label={t('Name')} rules={[{ required: true, message: t('Enter a name') }]}>
            <Input placeholder={t('e.g. internal gateway client certificate')} />
          </Form.Item>
          <Form.Item name="type" label={t('Type')} rules={[{ required: true }]}>
            <Select
              options={[
                { value: 'pem', label: 'PEM' },
                { value: 'p12', label: 'P12 / PKCS#12' },
              ]}
            />
          </Form.Item>
          <Form.Item name="cert_ref" label={t('Cert reference (cert_ref)')}>
            <Input placeholder="artifact://... 或密钥后端引用" />
          </Form.Item>
          <Form.Item name="key_ref" label={t('Private key reference (key_ref)')}>
            <Input placeholder="artifact://... 或密钥后端引用" />
          </Form.Item>
          <Form.Item name="password_secret_ref" label={t('Password reference (password_secret_ref)')}>
            <Input placeholder="vault://... / secret_ref" />
          </Form.Item>
          <Form.Item name="description" label={t('Description')}>
            <Input.TextArea rows={3} />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
