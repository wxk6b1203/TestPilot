import { Button, Card, Form, Input, Modal, Popconfirm, Select, Space } from 'antd'
import { DeleteOutlined, PlayCircleOutlined, PlusOutlined } from '@ant-design/icons'
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { del, get, post } from '../api'
import type { ListResp, TestPlan } from '../api'
import IdeLayout from '../components/IdeLayout'
import PanelList from '../components/PanelList'
import { PALETTE } from '../theme'
import { useLayout } from '../hooks/useLayout'
import PlanEditor from './PlanEditor'
import { message } from '../messageBridge'
import { t } from '../i18n'

// 测试计划列表：左侧面板为计划列表（运行/删除/新建），右侧为编辑器（/plans/:id/edit）。
export default function Plans() {
  const { projectId, envId, envs } = useLayout()
  const { id } = useParams()
  const nav = useNavigate()
  const [rows, setRows] = useState<TestPlan[]>([])
  const [search, setSearch] = useState('')
  const [open, setOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [running, setRunning] = useState('')
  const [form] = Form.useForm()

  const load = () =>
    projectId
      ? get<ListResp<TestPlan>>(`/api/v1/plans?project_id=${projectId}&page_size=500`).then((r) => {
          setRows(r.items)
        })
      : Promise.resolve()

  useEffect(() => {
    setRows([])
    load().catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>{t('Select a project at the top first')}</Card>

  const filtered = rows.filter((p) => p.name.toLowerCase().includes(search.trim().toLowerCase()))

  const runPlan = async (id: string) => {
    setRunning(id)
    try {
      const r = await post<{ run_id: string }>(`/api/v1/plans/${id}/run`, {})
      message.success(t('Run triggered: {id}', { id: r.run_id }))
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setRunning('')
    }
  }

  return (
    <IdeLayout
      panel={
        <PanelList
          title={t('Test plans')}
          search={search}
          onSearch={setSearch}
          extra={
            <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
              {t('New')}
            </Button>
          }
          data={filtered}
          onPick={(p) => nav(`/plans/${p.id}/edit`)}
          renderItem={(p) => (
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
              <div style={{ minWidth: 0, flex: 1 }}>
                <div
                  style={{
                    fontSize: 13, fontWeight: 500, color: PALETTE.text,
                    overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                  }}
                >
                  {p.name}
                </div>
                <div style={{ fontSize: 12, color: PALETTE.textSecondary, marginTop: 2 }}>
                  {envs.find((e) => e.id === p.env_id)?.name || t('No environment set')}
                </div>
              </div>
              <Space size={4} onClick={(e) => e.stopPropagation()}>
                <Button size="small" type="primary" loading={running === p.id} onClick={() => runPlan(p.id)}>
                  {t('Run')}
                </Button>
                <Popconfirm
                  title={t('Delete this plan?')}
                  description={t('This cannot be undone')}
                  onConfirm={async () => {
                    try {
                      await del(`/api/v1/plans/${p.id}`)
                      message.success(t('Deleted'))
                      load()
                    } catch (e: any) {
                      message.error(e.message)
                    }
                  }}
                >
                  <Button size="small" danger type="text" icon={<DeleteOutlined />} />
                </Popconfirm>
              </Space>
            </div>
          )}
        />
      }
    >
      {id ? (
        <PlanEditor key={id} />
      ) : (
        <div
          style={{
            display: 'flex', height: '100%', flexDirection: 'column',
            alignItems: 'center', justifyContent: 'center', gap: 12,
          }}
        >
          <PlayCircleOutlined style={{ fontSize: 40, color: PALETTE.textTertiary }} />
          <div style={{ fontSize: 13, color: PALETTE.textTertiary }}>
            {t('Pick a plan on the left to edit, or click "+ New" to create one')}
          </div>
        </div>
      )}

      <Modal
        title={t('New test plan')}
        open={open}
        width={480}
        okText={t('Create')}
        confirmLoading={creating}
        onCancel={() => setOpen(false)}
        onOk={() => form.submit()}
        destroyOnHidden
      >
        <Form
          form={form}
          layout="vertical"
          initialValues={{ env_id: envId || undefined }}
          onFinish={async (v: { name: string; env_id: string }) => {
            setCreating(true)
            try {
              const r = await post<TestPlan>('/api/v1/plans', {
                project_id: projectId,
                env_id: v.env_id,
                name: v.name,
                concurrency: 1,
                timeout_ms: 300000,
                items: [],
              })
              message.success(t('Created'))
              setOpen(false)
              form.resetFields()
              nav(`/plans/${r.id}/edit`)
            } catch (e: any) {
              message.error(e.message)
            } finally {
              setCreating(false)
            }
          }}
        >
          <Form.Item name="name" label={t('Name')} rules={[{ required: true, message: t('Enter a plan name') }]}>
            <Input placeholder={t('Plan name')} />
          </Form.Item>
          <Form.Item name="env_id" label={t('Environment')} rules={[{ required: true, message: t('Select an environment') }]}>
            <Select
              placeholder={t('Select environment')}
              options={envs.map((e) => ({ value: e.id, label: `${e.name} (${e.base_url})` }))}
            />
          </Form.Item>
        </Form>
      </Modal>
    </IdeLayout>
  )
}
