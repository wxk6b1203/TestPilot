import { Button, Card, Form, Input, Modal, Popconfirm, Select, Space, message } from 'antd'
import { DeleteOutlined, PlayCircleOutlined, PlusOutlined } from '@ant-design/icons'
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { del, get, post } from '../api'
import type { ListResp, TestPlan } from '../api'
import IdeLayout from '../components/IdeLayout'
import PanelList from '../components/PanelList'
import { PALETTE } from '../theme'
import { useLayout } from './Layout'

// 测试计划列表：左侧面板为计划列表（运行/删除/新建），点击进入编辑器。
export default function Plans() {
  const { projectId, envId, envs } = useLayout()
  const nav = useNavigate()
  const [rows, setRows] = useState<TestPlan[]>([])
  const [search, setSearch] = useState('')
  const [open, setOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [running, setRunning] = useState('')
  const [form] = Form.useForm()

  const load = () =>
    projectId
      ? get<ListResp<TestPlan>>(`/api/v1/plans?project_id=${projectId}&page_size=200`).then((r) =>
          setRows(r.items),
        )
      : Promise.resolve()

  useEffect(() => {
    setRows([])
    load().catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  const filtered = rows.filter((p) => p.name.toLowerCase().includes(search.trim().toLowerCase()))

  const runPlan = async (id: string) => {
    setRunning(id)
    try {
      const r = await post<{ run_id: string }>(`/api/v1/plans/${id}/run`, {})
      message.success(`已触发运行 ${r.run_id}`)
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
          title="测试计划"
          search={search}
          onSearch={setSearch}
          extra={
            <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
              新建
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
                  {envs.find((e) => e.id === p.env_id)?.name || '未设置环境'}
                </div>
              </div>
              <Space size={4} onClick={(e) => e.stopPropagation()}>
                <Button size="small" type="primary" loading={running === p.id} onClick={() => runPlan(p.id)}>
                  运行
                </Button>
                <Popconfirm
                  title="删除计划？"
                  description="删除后不可恢复"
                  onConfirm={async () => {
                    try {
                      await del(`/api/v1/plans/${p.id}`)
                      message.success('已删除')
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
      <div
        style={{
          display: 'flex', height: '100%', flexDirection: 'column',
          alignItems: 'center', justifyContent: 'center', gap: 12,
        }}
      >
        <PlayCircleOutlined style={{ fontSize: 40, color: PALETTE.textTertiary }} />
        <div style={{ fontSize: 13, color: PALETTE.textTertiary }}>
          在左侧选择计划进行编辑，或点击「+ 新建」创建测试计划
        </div>
      </div>

      <Modal
        title="新建测试计划"
        open={open}
        width={480}
        okText="创建"
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
              message.success('已创建')
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
          <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入计划名称' }]}>
            <Input placeholder="计划名称" />
          </Form.Item>
          <Form.Item name="env_id" label="环境" rules={[{ required: true, message: '请选择环境' }]}>
            <Select
              placeholder="选择环境"
              options={envs.map((e) => ({ value: e.id, label: `${e.name} (${e.base_url})` }))}
            />
          </Form.Item>
        </Form>
      </Modal>
    </IdeLayout>
  )
}
