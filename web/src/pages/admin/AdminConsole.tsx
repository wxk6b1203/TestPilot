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
  ApiToken, AuditLog, IdentityProvider, ListResp, Member, NotificationChannel, Schedule,
  TenantQuota, TenantSetting, TestPlan, Environment,
} from '../../api'
import { PALETTE } from '../../theme'
import { useLayout } from '../../hooks/useLayout'
import { message } from '../../messageBridge'
import { t } from '../../i18n'

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
      message.success(t('Role updated'))
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
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setInvite(true)}>{t('Add member')}</Button>
      </Space>
      <Table
        size="small" rowKey="user_id" dataSource={items}
        pagination={false}
        columns={[
          { title: t('Username'), dataIndex: 'username' },
          { title: t('Display name'), dataIndex: 'display_name' },
          { title: t('Role'), dataIndex: 'role', render: (r: number) => ROLE_NAMES[r] ?? r },
          {
            title: t('Actions'), render: (_, row) => (
              <Space>
                <Select
                  size="small" style={{ width: 110 }} value={row.role}
                  options={[1, 2, 3, 4].map((v) => ({ value: v, label: ROLE_NAMES[v] }))}
                  onChange={(v) => setRole(row.user_id, v)}
                />
                <Popconfirm title={t('Remove this member?')} onConfirm={() => remove(row.user_id)}>
                  <Button size="small" danger>{t('Remove')}</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={t('Add member')} open={invite} onCancel={() => setInvite(false)}
        onOk={async () => {
          const v = await form.validateFields()
          try {
            await post('/api/v1/tenant/members', v)
            message.success(t('Added'))
            setInvite(false)
            form.resetFields()
            load()
          } catch (e: any) { message.error(e.message) }
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="username" label={t('Username')} rules={[{ required: true }]}>
            <Input placeholder={t('Created automatically if not exists (default password changeme123)')} />
          </Form.Item>
          <Form.Item name="role" label={t('Role')} initialValue={3}>
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
        { title: t('Quota'), dataIndex: 'metric' },
        { title: t('Used'), dataIndex: 'used' },
        {
          title: t('Limit'), dataIndex: 'limit',
          render: (v: number, row) => (
            <InputNumber
              size="small" value={v || undefined} placeholder={t('No limit')}
              onBlur={async (e) => {
                const val = Number(e.target.value) || 0
                try {
                  await put(`/api/v1/tenant/quotas/${row.metric}`, { limit: val })
                  message.success(t('Saved'))
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
          placeholder={t('key ([A-Za-z0-9_.-])')} />
        <Input size="small" style={{ width: 200 }} value={value} onChange={(e) => setValue(e.target.value)}
          placeholder="value" />
        <Button
          size="small" type="primary"
          onClick={async () => {
            try {
              await put(`/api/v1/tenant/settings/${key}`, { value })
              message.success(t('Saved'))
              setKey('')
              setValue('')
              load()
            } catch (e: any) { message.error(e.message) }
          }}
        >
          {t('Save')}
        </Button>
      </Space>
      <Table
        size="small" rowKey="key" dataSource={items} pagination={false}
        columns={[
          { title: 'Key', dataIndex: 'key' },
          { title: 'Value', dataIndex: 'value' },
          {
            title: t('Actions'), render: (_, row) => (
              <Popconfirm title={t('Delete this item?')} onConfirm={async () => {
                try { await del(`/api/v1/tenant/settings/${row.key}`); load() }
                catch (e: any) { message.error(e.message) }
              }}>
                <Button size="small" danger>{t('Delete')}</Button>
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
        <Button type="primary" icon={<PlusOutlined />} onClick={() => open()}>{t('New identity provider')}</Button>
      </Space>
      <Table
        size="small" rowKey="id" dataSource={items} pagination={false}
        columns={[
          { title: t('Name'), dataIndex: 'name' },
          { title: t('Type'), dataIndex: 'type', render: (v: string) => <Tag color={v === 'oauth2' ? 'orange' : 'blue'}>{v}</Tag> },
          { title: 'Issuer', dataIndex: 'issuer' },
          { title: t('Enabled'), dataIndex: 'enabled', render: (v: boolean) => (v ? '✓' : '—') },
          {
            title: t('Actions'), render: (_, row) => (
              <Space>
                <Button size="small" onClick={() => open(row)}>{t('Edit')}</Button>
                <Popconfirm title={t('Delete?')} onConfirm={async () => {
                  try { await del(`/api/v1/identity-providers/${row.id}`); load() }
                  catch (e: any) { message.error(e.message) }
                }}>
                  <Button size="small" danger>{t('Delete')}</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={editing ? t('Edit identity provider') : t('New identity provider')} open={modal}
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
          <Form.Item name="name" label={t('Name')} rules={[{ required: true }]}>
            <Input placeholder={t('e.g. Corporate SSO')} />
          </Form.Item>
          <Form.Item name="type" label={t('Type')} rules={[{ required: true }]}>
            <Select options={[
              { value: 'oidc', label: t('oidc (id_token signature verification)') },
              { value: 'oauth2', label: t('oauth2 (userinfo identity)') },
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
            <Input.Password placeholder={editing ? t('Leave empty to keep unchanged') : ''} />
          </Form.Item>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            {t('For OAuth2 providers without a discovery document, fill the endpoints (e.g. GitHub):')}
          </Typography.Text>
          <Form.Item name="authorization_endpoint" label="Authorization Endpoint" style={{ marginTop: 8 }}>
            <Input placeholder={t('Optional')} />
          </Form.Item>
          <Form.Item name="token_endpoint" label="Token Endpoint">
            <Input placeholder={t('Optional')} />
          </Form.Item>
          <Form.Item name="userinfo_endpoint" label="UserInfo Endpoint">
            <Input placeholder={t('Optional (required for oauth2 without discovery)')} />
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
// getter 每次读取求值：语言切换即时生效
  const TYPE: Record<number, string> = {
    get 1() { return 'Webhook' }, get 2() { return t('DingTalk') }, get 3() { return t('Feishu') },
  }
  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setModal(true)}>{t('New channel')}</Button>
      </Space>
      <Table
        size="small" rowKey="id" dataSource={items} pagination={false}
        columns={[
          { title: t('Name'), dataIndex: 'name' },
          { title: t('Type'), dataIndex: 'type', render: (v: number) => TYPE[v] ?? v },
          { title: t('Events'), dataIndex: 'events' },
          {
            title: t('Actions'), render: (_, row) => (
              <Popconfirm title={t('Delete?')} onConfirm={async () => {
                try { await del(`/api/v1/notifications/${row.id}`); load() }
                catch (e: any) { message.error(e.message) }
              }}>
                <Button size="small" danger>{t('Delete')}</Button>
              </Popconfirm>
            ),
          },
        ]}
      />
      <Modal
        title={t('New notification channel')} open={modal} onCancel={() => setModal(false)}
        onOk={async () => {
          const v = await form.validateFields()
          try {
            await post('/api/v1/notifications', v)
            setModal(false); form.resetFields(); load()
          } catch (e: any) { message.error(e.message) }
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label={t('Name')} rules={[{ required: true }]}><Input /></Form.Item>
          <Form.Item name="type" label={t('Type')} initialValue={1} rules={[{ required: true }]}>
            <Select options={[1, 2, 3].map((v) => ({ value: v, label: TYPE[v] }))} />
          </Form.Item>
          <Form.Item name="webhook_url" label="Webhook URL" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item name="events" label={t('Events (comma-separated)')} initialValue="run_finished,stress_finished">
            <Input />
          </Form.Item>
          <Form.Item name="secret" label={t('Signing secret (DingTalk/Feishu)')}>
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
  // null = 新建；非 null = 编辑该条
  const [editing, setEditing] = useState<Schedule | null>(null)
  const [saving, setSaving] = useState(false)
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

  const planOf = (id: string) => plans.find((p) => p.id === id)
  const envName = (id?: string) =>
    id && id !== '0' ? envs.find((e) => e.id === id)?.name : undefined
  // 实际运行环境：调度指定 env 优先，否则回落计划默认 env（与 runner.Trigger 语义一致）
  const envOf = (row: Schedule) => envName(row.env_id) ?? envName(planOf(row.plan_id)?.env_id)
  const fmtTime = (v?: string) => (v ? new Date(v).toLocaleString() : '—')

  const openCreate = () => {
    setEditing(null)
    form.resetFields()
    setModal(true)
  }
  const openEdit = (row: Schedule) => {
    setEditing(row)
    form.setFieldsValue({
      plan_id: row.plan_id,
      env_id: envName(row.env_id) ? row.env_id : undefined,
      name: row.name,
      cron_expr: row.cron_expr,
      overlap_policy: row.overlap_policy,
    })
    setModal(true)
  }
  const save = async () => {
    const v = await form.validateFields()
    setSaving(true)
    try {
      if (editing) {
        // 后端语义：env_id "0" = 取消指定环境（回落计划默认）；name 空串 = 保持不变
        await put(`/api/v1/schedules/${editing.id}`, { ...v, env_id: v.env_id ?? '0' })
      } else {
        await post('/api/v1/schedules', v)
      }
      setModal(false)
      form.resetFields()
      load()
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setSaving(false)
    }
  }
  const toggle = async (row: Schedule) => {
    try {
      await put(`/api/v1/schedules/${row.id}`, { enabled: !row.enabled })
      load()
    } catch (e: any) { message.error(e.message) }
  }

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>{t('New schedule')}</Button>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('Runs the selected test plan automatically on a cron schedule, same as clicking Run; filter run history by trigger on the Runs page')}
        </Typography.Text>
      </Space>
      <Table
        size="small" rowKey="id" dataSource={items} pagination={false}
        columns={[
          { title: t('Name'), dataIndex: 'name', render: (v: string) => v || '—' },
          { title: t('Plan'), dataIndex: 'plan_id', render: (v: string) => planOf(v)?.name ?? v },
          { title: t('Environment'), render: (_, row) => envOf(row) ?? '—' },
          { title: 'Cron', dataIndex: 'cron_expr' },
          { title: t('Overlap policy'), dataIndex: 'overlap_policy', render: (v: number) => (v === 1 ? t('Skip') : t('Concurrent')) },
          { title: t('Last run'), dataIndex: 'last_run_at', render: fmtTime },
          { title: t('Next run'), dataIndex: 'next_run_at', render: fmtTime },
          { title: t('Enabled'), dataIndex: 'enabled', render: (v: boolean) => (v ? '✓' : '—') },
          {
            title: t('Actions'), render: (_, row) => (
              <Space>
                <Button size="small" onClick={() => openEdit(row)}>{t('Edit')}</Button>
                <Button size="small" onClick={() => toggle(row)}>
                  {row.enabled ? t('Disable') : t('Enable')}
                </Button>
                <Popconfirm title={t('Delete?')} onConfirm={async () => {
                  try { await del(`/api/v1/schedules/${row.id}`); load() }
                  catch (e: any) { message.error(e.message) }
                }}>
                  <Button size="small" danger>{t('Delete')}</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Modal
        title={editing ? t('Edit schedule') : t('New schedule')} open={modal}
        onCancel={() => setModal(false)} onOk={save} confirmLoading={saving}
        okText={editing ? t('Save') : t('Create')}
      >
        <Form form={form} layout="vertical">
          <Form.Item
            name="plan_id" label={t('Plan')} rules={[{ required: true }]}
            extra={plans.length === 0 ? t('No test plans in this project yet: create one on the Plans page first, or switch projects from the top bar') : undefined}
          >
            <Select
              placeholder={plans.length === 0 ? t('No plans available') : t('Select the plan to run on schedule')}
              options={plans.map((p) => ({ value: p.id, label: p.name }))}
            />
          </Form.Item>
          <Form.Item
            name="env_id" label={t('Environment')} extra={t('Leave empty to use the plan\'s default environment')}
          >
            <Select allowClear options={envs.map((e) => ({ value: e.id, label: e.name }))} />
          </Form.Item>
          <Form.Item name="name" label={t('Name')}>
            <Input placeholder={t('e.g. Weekday smoke regression')} maxLength={100} />
          </Form.Item>
          <Form.Item
            name="cron_expr" label={t('Cron expression')}
            rules={[{ required: true }]}
            extra={t('Standard 5-field expression: minute hour day month weekday; 0 9 * * 1-5 = weekdays at 9:00')}
          >
            <Input placeholder="0 9 * * 1-5" />
          </Form.Item>
          <Form.Item
            name="overlap_policy" label={t('Overlap policy')} initialValue={1}
            extra={t('When the previous run is still going: skip this tick, or allow concurrency')}
          >
            <Select options={[{ value: 1, label: t('Skip') }, { value: 2, label: t('Concurrent') }]} />
          </Form.Item>
          {!editing && (
            <Form.Item name="enabled" label={t('Enabled')} initialValue={true} valuePropName="checked">
              <Switch />
            </Form.Item>
          )}
        </Form>
      </Modal>
    </div>
  )
}

function ApiTokensTab() {
  const [items, setItems] = useState<ApiToken[]>([])
  const [modal, setModal] = useState(false)
  const [created, setCreated] = useState('')
  const [form] = Form.useForm()
  const load = () =>
    get<{ items: ApiToken[] }>('/api/v1/api-tokens').then((r) => setItems(r.items))
  useEffect(() => { load().catch(() => {}) }, [])

  const remove = async (id: string) => {
    try {
      await del(`/api/v1/api-tokens/${id}`)
      message.success(t('Deleted'))
      load()
    } catch (e: any) { message.error(e.message) }
  }

  return (
    <div>
      <Space style={{ marginBottom: 12 }}>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => { form.resetFields(); setModal(true) }}>
          {t('New token')}
        </Button>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('Machine credentials for CI/CLI: the raw token is shown only once at creation; save it now')}
        </Typography.Text>
      </Space>
      <Table
        size="small" rowKey="id" dataSource={items} pagination={false}
        columns={[
          { title: t('Name'), dataIndex: 'name' },
          {
            title: 'Scopes', dataIndex: 'scopes',
            render: (v: string[]) => (
              <Space size={4}>
                {(v || ['*']).map((s) => <Tag key={s} style={{ margin: 0 }}>{s}</Tag>)}
              </Space>
            ),
          },
          { title: t('Issuer'), dataIndex: 'user_id', width: 100, render: (v: string) => `#${v.slice(-8)}` },
          {
            title: t('Expires at'), dataIndex: 'expires_at', width: 160,
            render: (v?: string) => v ? new Date(v).toLocaleString() : t('Never expires'),
          },
          {
            title: t('Last used'), dataIndex: 'last_used_at', width: 160,
            render: (v?: string) => v ? new Date(v).toLocaleString() : '—',
          },
          {
            title: t('Actions'), width: 80,
            render: (_, row) => (
              <Popconfirm title={t('Delete this token? CIs using it will break immediately')} onConfirm={() => remove(row.id)}>
                <Button size="small" danger>{t('Delete')}</Button>
              </Popconfirm>
            ),
          },
        ]}
      />
      <Modal
        title={t('New API token')} open={modal}
        onCancel={() => setModal(false)}
        onOk={async () => {
          const v = await form.validateFields()
          const days = Number(v.expires_in_days) || 0
          const expires_at = days > 0
            ? new Date(Date.now() + days * 86400_000).toISOString()
            : ''
          try {
            const r = await post<{ id: string; token: string }>('/api/v1/api-tokens', {
              name: v.name,
              expires_at,
              scopes: String(v.scopes ?? '*').split(',').map((s: string) => s.trim()).filter(Boolean),
            })
            setCreated(r.token)
            setModal(false)
            load()
          } catch (e: any) { message.error(e.message) }
        }}
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label={t('Name')} rules={[{ required: true }]}>
            <Input placeholder={t('e.g. jenkins-ci / gitlab-runner')} />
          </Form.Item>
          <Form.Item name="expires_in_days" label={t('Valid for (days, 0 = never expires)')} initialValue={0}>
            <InputNumber min={0} max={3650} style={{ width: 160 }} />
          </Form.Item>
          <Form.Item name="scopes" label={t('Scopes (comma-separated; informational for now)')} initialValue="*">
            <Input placeholder="*" />
          </Form.Item>
        </Form>
      </Modal>
      <Modal
        title={t('Token created (shown only once)')}
        open={!!created}
        onCancel={() => setCreated('')}
        footer={<Button type="primary" onClick={() => setCreated('')}>{t('I have saved it')}</Button>}
      >
        <Typography.Paragraph>
          {t('This credential is visible only once; copy it into your CI secret store / local vault:')}
        </Typography.Paragraph>
        <Typography.Paragraph code copyable={{ text: created }} style={{ wordBreak: 'break-all' }}>
          {created}
        </Typography.Paragraph>
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {t('Usage: Authorization: Bearer {prefix}…; permissions follow the issuer\'s current tenant role.', { prefix: created.slice(0, 12) })}
        </Typography.Text>
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
        { title: t('Time'), dataIndex: 'created_at', render: (v: string) => new Date(v).toLocaleString() },
        { title: t('Actor'), dataIndex: 'actor', render: (v: number) => (v === 2 ? 'Copilot' : t('Human')), width: 80 },
        { title: t('Action'), dataIndex: 'action', width: 140 },
        { title: t('Resource'), dataIndex: 'resource_type', width: 100 },
        { title: t('Resource ID'), dataIndex: 'resource_id', width: 120 },
        {
          title: t('Detail'), dataIndex: 'detail',
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
    { key: 'members', label: t('Members'), children: <MembersTab /> },
    { key: 'quotas', label: t('Quotas'), children: <QuotasTab /> },
    { key: 'settings', label: t('Settings'), children: <SettingsTab /> },
    { key: 'tokens', label: 'API Token', children: <ApiTokensTab /> },
    { key: 'idp', label: t('Identity providers'), children: <IdpTab /> },
    { key: 'notifications', label: t('Notifications'), children: <NotificationsTab /> },
    { key: 'schedules', label: t('Schedules'), children: <SchedulesTab /> },
    { key: 'audit', label: t('Audit log'), children: <AuditTab /> },
  ], [])
  return (
    <div style={{ padding: 16, background: PALETTE.bgLayout, minHeight: '100%' }}>
      <Tabs items={tabs} />
    </div>
  )
}
