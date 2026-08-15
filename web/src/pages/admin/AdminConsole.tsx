import { useEffect, useMemo, useState } from 'react'
import {
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tabs,
  Tag,
  Typography,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { del, get, post, put } from '../../api'
import type {
  AuditLog, IdentityProvider, ListResp, Member, NotificationChannel, Schedule, TenantQuota,
  TenantSetting, TestPlan, Environment,
} from '../../api'
import { PALETTE } from '../../theme'
import { useLayout } from '../../hooks/useLayout'
import { message } from '../../messageBridge'

// 租户管理台（admin+）：成员 / 配额 / 设置 / 身份源 / 通知 / 定时任务 / 审计日志

const ROLE_NAMES: Record<number, string> = { 1: 'owner', 2: 'admin', 3: 'member', 4: 'viewer' }

function MembersTab() {
  const [items, setItems] = useState<Member[]>([])
  const load = () =>
    get<{ items: Member[] }>('/api/v1/tenant/members').then((r) => setItems(r.items))
  useEffect(() => { load().catch(() => {}) }, [])
  const setRole = async (userID: string, role: number) => {
    try {
      await put(`/api/v1/tenant/members/${userID}`, { role })
      message.success('角色已更新')
      load()
    } catch (e: any) { message.error(e.message) }
  }
  const remove = async (userID: string) => {
    try {
      await del(`/api/v1/tenant/members/${userID}`)
      load()
    } catch (e: any) { message.error(e.message) } // LAST_OWNER 等错误原样展示
  }
  const [invite, setInvite] = useState(false)
  const [form] = Form.useForm()
  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setInvite(true)}>添加成员</Button>
      </Space>
      <Table
        size="small" rowKey="user_id" dataSource={items}
        pagination={false}
        columns={[
          { title: '用户名', dataIndex: 'username' },
          { title: '昵称', dataIndex: 'display_name' },
          { title: '角色', dataIndex: 'role', render: (r: number) => ROLE_NAMES[r] ?? r },
          {
            title: '操作', render: (_, row) => (
              <Space>
                <Select
                  size="small" style={{ width: 110 }} value={row.role}
                  options={[1, 2, 3, 4].map((v) => ({ value: v, label: ROLE_NAMES[v] }))}
                  onChange={(v) => setRole(row.user_id, v)}
                />
                <Popconfirm title="移除该成员？" onConfirm={() => remove(row.user_id)}>
                  <Button size="small" danger>移除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title="添加成员" open={invite} onCancel={() => setInvite(false)}
        onOk={async () => {
          const v = await form.validateFields()
          try {
            await post('/api/v1/tenant/members', v)
            message.success('已添加')
            setInvite(false)
            form.resetFields()
            load()
          } catch (e: any) { message.error(e.message) }
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="username" label="用户名" rules={[{ required: true }]}>
            <Input placeholder="不存在则自动创建（默认密码 changeme123）" />
          </Form.Item>
          <Form.Item name="role" label="角色" initialValue={3}>
            <Select options={[1, 2, 3, 4].map((v) => ({ value: v, label: ROLE_NAMES[v] }))} />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function QuotasTab() {
  const [items, setItems] = useState<TenantQuota[]>([])
  const load = () =>
    get<{ items: TenantQuota[] }>('/api/v1/tenant/quotas').then((r) => setItems(r.items))
  useEffect(() => { load().catch(() => {}) }, [])
  return (
    <Table
      size="small" rowKey="metric" dataSource={items} pagination={false}
      columns={[
        { title: '配额项', dataIndex: 'metric' },
        { title: '已用', dataIndex: 'used' },
        {
          title: '上限', dataIndex: 'limit',
          render: (v: number, row) => (
            <InputNumber
              size="small" value={v || undefined} placeholder="不限"
              onBlur={async (e) => {
                const val = Number(e.target.value) || 0
                try {
                  await put(`/api/v1/tenant/quotas/${row.metric}`, { limit: val })
                  message.success('已保存')
                  load()
                } catch (err: any) { message.error(err.message) }
              }}
            />
          ),
        },
      ]}
    />
  )
}

function SettingsTab() {
  const [items, setItems] = useState<TenantSetting[]>([])
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const load = () =>
    get<{ items: TenantSetting[] }>('/api/v1/tenant/settings').then((r) => setItems(r.items))
  useEffect(() => { load().catch(() => {}) }, [])
  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Input size="small" style={{ width: 200 }} value={key} onChange={(e) => setKey(e.target.value)}
          placeholder="key（[A-Za-z0-9_.-]）" />
        <Input size="small" style={{ width: 200 }} value={value} onChange={(e) => setValue(e.target.value)}
          placeholder="value" />
        <Button
          size="small" type="primary"
          onClick={async () => {
            try {
              await put(`/api/v1/tenant/settings/${key}`, { value })
              message.success('已保存')
              setKey('')
              setValue('')
              load()
            } catch (e: any) { message.error(e.message) }
          }}
        >
          保存
        </Button>
      </Space>
      <Table
        size="small" rowKey="key" dataSource={items} pagination={false}
        columns={[
          { title: 'Key', dataIndex: 'key' },
          { title: 'Value', dataIndex: 'value' },
          {
            title: '操作', render: (_, row) => (
              <Popconfirm title="删除该项？" onConfirm={async () => {
                try { await del(`/api/v1/tenant/settings/${row.key}`); load() }
                catch (e: any) { message.error(e.message) }
              }}>
                <Button size="small" danger>删除</Button>
              </Popconfirm>
            ),
          },
        ]}
      />
    </div>
  )
}

function IdpTab() {
  const [items, setItems] = useState<IdentityProvider[]>([])
  const [modal, setModal] = useState(false)
  const [editing, setEditing] = useState<IdentityProvider | null>(null)
  const [form] = Form.useForm()
  const load = () =>
    get<{ items: IdentityProvider[] }>('/api/v1/identity-providers').then((r) => setItems(r.items))
  useEffect(() => { load().catch(() => {}) }, [])
  const open = (p?: IdentityProvider) => {
    setEditing(p ?? null)
    form.setFieldsValue(p ? {
      name: p.name, type: p.type, issuer: p.issuer, client_id: p.client_id,
      client_secret: '', authorization_endpoint: p.config?.authorization_endpoint ?? '',
      token_endpoint: p.config?.token_endpoint ?? '', userinfo_endpoint: p.config?.userinfo_endpoint ?? '',
    } : { type: 'oidc', client_secret: '' })
    setModal(true)
  }
  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => open()}>新建身份源</Button>
      </Space>
      <Table
        size="small" rowKey="id" dataSource={items} pagination={false}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '类型', dataIndex: 'type', render: (t: string) => <Tag color={t === 'oauth2' ? 'orange' : 'blue'}>{t}</Tag> },
          { title: 'Issuer', dataIndex: 'issuer' },
          { title: '启用', dataIndex: 'enabled', render: (v: boolean) => (v ? '✓' : '—') },
          {
            title: '操作', render: (_, row) => (
              <Space>
                <Button size="small" onClick={() => open(row)}>编辑</Button>
                <Popconfirm title="删除？" onConfirm={async () => {
                  try { await del(`/api/v1/identity-providers/${row.id}`); load() }
                  catch (e: any) { message.error(e.message) }
                }}>
                  <Button size="small" danger>删除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={editing ? '编辑身份源' : '新建身份源'} open={modal}
        onCancel={() => setModal(false)}
        onOk={async () => {
          const v = await form.validateFields()
          const payload = { ...v }
          if (editing) {
            try {
              await put(`/api/v1/identity-providers/${editing.id}`, payload)
              setModal(false); load()
            } catch (e: any) { message.error(e.message) }
          } else {
            try {
              await post('/api/v1/identity-providers', payload)
              setModal(false); load()
            } catch (e: any) { message.error(e.message) }
          }
        }}
        width={560}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true }]}>
            <Input placeholder="如 企业 SSO" />
          </Form.Item>
          <Form.Item name="type" label="类型" rules={[{ required: true }]}>
            <Select options={[
              { value: 'oidc', label: 'oidc（id_token 验签）' },
              { value: 'oauth2', label: 'oauth2（userinfo 身份）' },
            ]} />
          </Form.Item>
          <Form.Item name="issuer" label="Issuer" rules={[{ required: true }]}>
            <Input placeholder="https://idp.example.com" />
          </Form.Item>
          <Form.Item name="client_id" label="Client ID" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="client_secret" label="Client Secret"
            rules={editing ? [] : [{ required: true }]}>
            <Input.Password placeholder={editing ? '留空保持不变' : ''} />
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            OAuth2 提供方无 discovery 文档时填端点（如 GitHub）：
          </Typography.Text>
          <Form.Item name="authorization_endpoint" label="Authorization Endpoint" style={{ marginTop: 8 }}>
            <Input placeholder="可选" />
          </Form.Item>
          <Form.Item name="token_endpoint" label="Token Endpoint">
            <Input placeholder="可选" />
          </Form.Item>
          <Form.Item name="userinfo_endpoint" label="UserInfo Endpoint">
            <Input placeholder="可选（oauth2 必需或走 discovery）" />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function NotificationsTab() {
  const [items, setItems] = useState<NotificationChannel[]>([])
  const [modal, setModal] = useState(false)
  const [form] = Form.useForm()
  const load = () =>
    get<{ items: NotificationChannel[] }>('/api/v1/notifications').then((r) => setItems(r.items))
  useEffect(() => { load().catch(() => {}) }, [])
  const TYPE: Record<number, string> = { 1: 'Webhook', 2: '钉钉', 3: '飞书' }
  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModal(true)}>新建渠道</Button>
      </Space>
      <Table
        size="small" rowKey="id" dataSource={items} pagination={false}
        columns={[
          { title: '名称', dataIndex: 'name' },
          { title: '类型', dataIndex: 'type', render: (t: number) => TYPE[t] ?? t },
          { title: '事件', dataIndex: 'events' },
          {
            title: '操作', render: (_, row) => (
              <Popconfirm title="删除？" onConfirm={async () => {
                try { await del(`/api/v1/notifications/${row.id}`); load() }
                catch (e: any) { message.error(e.message) }
              }}>
                <Button size="small" danger>删除</Button>
              </Popconfirm>
            ),
          },
        ]}
      />
      <Modal
        title="新建通知渠道" open={modal} onCancel={() => setModal(false)}
        onOk={async () => {
          const v = await form.validateFields()
          try {
            await post('/api/v1/notifications', v)
            setModal(false); form.resetFields(); load()
          } catch (e: any) { message.error(e.message) }
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="type" label="类型" initialValue={1} rules={[{ required: true }]}>
            <Select options={[1, 2, 3].map((v) => ({ value: v, label: TYPE[v] }))} />
          </Form.Item>
          <Form.Item name="webhook_url" label="Webhook URL" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="events" label="事件（逗号分隔）" initialValue="run_finished,stress_finished">
            <Input />
          </Form.Item>
          <Form.Item name="secret" label="加签密钥（钉钉/飞书）">
            <Input.Password />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function SchedulesTab() {
  const { projectId } = useLayout()
  const [items, setItems] = useState<Schedule[]>([])
  const [plans, setPlans] = useState<TestPlan[]>([])
  const [envs, setEnvs] = useState<Environment[]>([])
  const [modal, setModal] = useState(false)
  const [form] = Form.useForm()
  const load = () =>
    get<{ items: Schedule[] }>('/api/v1/schedules').then((r) => setItems(r.items))
  useEffect(() => {
    load().catch(() => {})
    // 计划下拉限定当前项目，避免定时任务指向其它项目的计划
    get<ListResp<TestPlan>>(projectId ? `/api/v1/plans?project_id=${projectId}&page_size=100` : '/api/v1/plans?page_size=100')
      .then((r) => setPlans(r.items)).catch(() => {})
    get<ListResp<Environment>>(projectId ? `/api/v1/environments?project_id=${projectId}&page_size=100` : '/api/v1/environments?page_size=100')
      .then((r) => setEnvs(r.items)).catch(() => {})
  }, [projectId])
  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModal(true)}>新建定时任务</Button>
      </Space>
      <Table
        size="small" rowKey="id" dataSource={items} pagination={false}
        columns={[
          { title: '计划', dataIndex: 'plan_id', render: (v: string) => plans.find((p) => p.id === v)?.name ?? v },
          { title: 'Cron', dataIndex: 'cron_expr' },
          { title: '重叠策略', dataIndex: 'overlap_policy', render: (v: number) => (v === 1 ? '跳过' : '并发') },
          { title: '启用', dataIndex: 'enabled', render: (v: boolean) => (v ? '✓' : '—') },
          {
            title: '操作', render: (_, row) => (
              <Space>
                <Button size="small" onClick={async () => {
                  try {
                    await put(`/api/v1/schedules/${row.id}`, { enabled: !row.enabled })
                    load()
                  } catch (e: any) { message.error(e.message) }
                }}>
                  {row.enabled ? '停用' : '启用'}
                </Button>
                <Popconfirm title="删除？" onConfirm={async () => {
                  try { await del(`/api/v1/schedules/${row.id}`); load() }
                  catch (e: any) { message.error(e.message) }
                }}>
                  <Button size="small" danger>删除</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title="新建定时任务" open={modal} onCancel={() => setModal(false)}
        onOk={async () => {
          const v = await form.validateFields()
          try {
            await post('/api/v1/schedules', v)
            setModal(false); form.resetFields(); load()
          } catch (e: any) { message.error(e.message) }
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="plan_id" label="计划" rules={[{ required: true }]}>
            <Select options={plans.map((p) => ({ value: p.id, label: p.name }))} />
          </Form.Item>
          <Form.Item name="env_id" label="环境">
            <Select allowClear options={envs.map((e) => ({ value: e.id, label: e.name }))} />
          </Form.Item>
          <Form.Item name="cron_expr" label="Cron 表达式" rules={[{ required: true }]}>
            <Input placeholder="0 9 * * 1-5" />
          </Form.Item>
          <Form.Item name="overlap_policy" label="重叠策略" initialValue={1}>
            <Select options={[{ value: 1, label: '跳过' }, { value: 2, label: '并发' }]} />
          </Form.Item>
          <Form.Item name="enabled" label="启用" initialValue={true} valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </div>
  )
}

function AuditTab() {
  const [items, setItems] = useState<AuditLog[]>([])
  const load = () =>
    get<{ items: AuditLog[] }>('/api/v1/audit-logs?page_size=200').then((r) => setItems(r.items))
  useEffect(() => { load().catch(() => {}) }, [])
  return (
    <Table
      size="small" rowKey="id" dataSource={items} pagination={{ pageSize: 20 }}
      columns={[
        { title: '时间', dataIndex: 'created_at', render: (v: string) => new Date(v).toLocaleString() },
        { title: '操作者', dataIndex: 'actor', render: (v: number) => (v === 2 ? 'Copilot' : '人工'), width: 80 },
        { title: '动作', dataIndex: 'action', width: 140 },
        { title: '资源', dataIndex: 'resource_type', width: 100 },
        { title: '资源 ID', dataIndex: 'resource_id', width: 120 },
        {
          title: '详情', dataIndex: 'detail',
          render: (v: any) => v ? (
            <pre style={{ margin: 0, fontSize: 11, color: PALETTE.textSecondary, maxWidth: 400, overflow: 'hidden' }}>
              {JSON.stringify(v)}
            </pre>
          ) : '—',
        },
      ]}
    />
  )
}

export default function AdminConsole() {
  const tabs = useMemo(() => [
    { key: 'members', label: '成员', children: <MembersTab /> },
    { key: 'quotas', label: '配额', children: <QuotasTab /> },
    { key: 'settings', label: '设置', children: <SettingsTab /> },
    { key: 'idp', label: '身份源', children: <IdpTab /> },
    { key: 'notifications', label: '通知', children: <NotificationsTab /> },
    { key: 'schedules', label: '定时任务', children: <SchedulesTab /> },
    { key: 'audit', label: '审计日志', children: <AuditTab /> },
  ], [])
  return (
    <div style={{ padding: 16, background: PALETTE.bgLayout, minHeight: '100%' }}>
      <Tabs items={tabs} />
    </div>
  )
}
