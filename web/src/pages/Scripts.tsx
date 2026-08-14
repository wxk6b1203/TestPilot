import { Button, Card, Empty, Input, Modal, Space, Tag, message } from 'antd'
import { ArrowLeftOutlined, PlusOutlined } from '@ant-design/icons'
import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, put } from '../api'
import type { ListResp, Script } from '../api'
import IdeLayout from '../components/IdeLayout'
import PanelList from '../components/PanelList'
import { PALETTE } from '../theme'
import { useLayout } from './Layout'

const SCRIPT_TEMPLATE = `async def run(ctx):
    # 沙箱内无网络出口：HTTP 经能力桥由 Worker 代执行
    resp = await ctx.http("GET", "/json")
    return resp.body
`

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ fontSize: 12, color: PALETTE.textSecondary, marginBottom: 4 }}>{label}</div>
      {children}
    </div>
  )
}

export default function Scripts() {
  const nav = useNavigate()
  const { id } = useParams()
  const { projectId } = useLayout()
  const [scripts, setScripts] = useState<Script[]>([])
  const [search, setSearch] = useState('')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [language, setLanguage] = useState('python')
  const [content, setContent] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [createName, setCreateName] = useState('')
  const editing = !!id

  const loadScripts = () =>
    projectId
      ? get<ListResp<Script>>(`/api/v1/scripts?project_id=${projectId}&page_size=200`).then((r) => setScripts(r.items))
      : Promise.resolve()

  useEffect(() => {
    setScripts([])
    if (!projectId) return
    loadScripts().catch((e) => message.error(e.message))
  }, [projectId])

  // 进入/切换编辑路由时加载脚本详情
  useEffect(() => {
    if (!id) {
      setName('')
      setDescription('')
      setLanguage('python')
      setContent('')
      return
    }
    loadScripts().catch(() => {})
    get<Script>(`/api/v1/scripts/${id}`)
      .then((s) => {
        setName(s.name || '')
        setDescription(s.description || '')
        setLanguage(s.language || 'python')
        setContent(s.content || '')
      })
      .catch((e) => message.error(e.message))
  }, [id])

  const filtered = useMemo(
    () => scripts.filter((s) => (s.name || '').toLowerCase().includes(search.toLowerCase())),
    [scripts, search],
  )

  const save = async () => {
    if (!name.trim()) {
      message.error('名称必填')
      return
    }
    if (!content.trim()) {
      message.error('内容必填')
      return
    }
    const payload = {
      project_id: projectId,
      name: name.trim(),
      description,
      language: language.trim() || 'python',
      content,
    }
    try {
      if (id) {
        await put(`/api/v1/scripts/${id}`, payload)
        message.success('已保存')
        loadScripts()
      } else {
        const r = await post<Script>('/api/v1/scripts', payload)
        message.success('已创建')
        nav(`/scripts/${r.id}/edit`)
      }
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const create = async () => {
    if (!createName.trim()) {
      message.error('请输入脚本名称')
      return
    }
    try {
      const r = await post<Script>('/api/v1/scripts', {
        project_id: projectId,
        name: createName.trim(),
        description: '',
        language: 'python',
        content: SCRIPT_TEMPLATE,
      })
      setCreateOpen(false)
      setCreateName('')
      message.success('已创建')
      nav(`/scripts/${r.id}/edit`)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  const panel = (
    <PanelList
      title="脚本"
      search={search}
      onSearch={setSearch}
      extra={(
        <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          新建
        </Button>
      )}
      data={filtered}
      activeId={id}
      onPick={(s) => nav(`/scripts/${s.id}/edit`)}
      renderItem={(s) => (
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>
            {s.name}
          </span>
          <Tag style={{ margin: 0 }} color={s.language === 'python' ? 'blue' : 'default'}>
            {s.language || 'python'}
          </Tag>
        </div>
      )}
    />
  )

  const toolbar = (
    <Space>
      <Button icon={<ArrowLeftOutlined />} onClick={() => nav('/scripts')}>返回</Button>
      <Button type="primary" onClick={save}>保存</Button>
    </Space>
  )

  const editor = (
    <div style={{ padding: 16, maxWidth: 1000 }}>
      <Field label="名称">
        <Input
          value={name} onChange={(e) => setName(e.target.value)}
          placeholder="脚本名称" style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label="描述">
        <Input
          value={description} onChange={(e) => setDescription(e.target.value)}
          placeholder="脚本描述" style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label="语言">
        <Input
          value={language} onChange={(e) => setLanguage(e.target.value)}
          placeholder="python" style={{ width: 160 }}
        />
      </Field>
      <Field label="内容">
        <Input.TextArea
          rows={18}
          style={{ fontFamily: 'monospace', fontSize: 12 }}
          placeholder="async def run(ctx): ..."
          value={content}
          onChange={(e) => setContent(e.target.value)}
        />
      </Field>
    </div>
  )

  const placeholder = (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 48, gap: 12 }}>
      <Empty description="从左侧选择脚本，或新建一个脚本" />
      <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>新建脚本</Button>
    </div>
  )

  return (
    <>
      <IdeLayout panel={panel} toolbar={editing ? toolbar : undefined}>
        {editing ? editor : placeholder}
      </IdeLayout>
      <Modal
        title="新建脚本"
        open={createOpen}
        okText="创建"
        onCancel={() => setCreateOpen(false)}
        onOk={create}
        destroyOnHidden
      >
        <Input
          value={createName}
          onChange={(e) => setCreateName(e.target.value)}
          onPressEnter={create}
          placeholder="脚本名称"
        />
      </Modal>
    </>
  )
}
