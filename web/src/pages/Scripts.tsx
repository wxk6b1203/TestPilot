import { Button, Card, Empty, Input, Modal, Popconfirm, Space, Tag, Typography } from 'antd'
import { ArrowLeftOutlined, DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { del, get, post, put } from '../api'
import type { ListResp, Script } from '../api'
import IdeLayout from '../components/IdeLayout'
import PanelList from '../components/PanelList'
import { PALETTE } from '../theme'
import useSaveShortcut from '../hooks/useSaveShortcut'
import { useLeaveGuard } from '../hooks/useLeaveGuard'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'
import { t } from '../i18n'

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
  const [saving, setSaving] = useState(false)
  // 已保存快照（dirty 判定 + 离开守卫）
  const [savedSnap, setSavedSnap] = useState(() => JSON.stringify({ n: '', d: '', l: 'python', c: '' }))
  const editing = !!id

  const loadScripts = () =>
    projectId
      ? get<ListResp<Script>>(`/api/v1/scripts?project_id=${projectId}&page_size=500`).then((r) => { setScripts(r.items) })
      : Promise.resolve()

  useEffect(() => {
    setScripts([])
    if (!projectId) return
    loadScripts().catch((e) => message.error(e.message))
  }, [projectId])

  // 进入/切换编辑路由时加载脚本详情。seq 防乱序：快速切换 A→B 时 A 的慢响应
  // 不得覆盖 B 的表单（组件不因 id 重挂载，覆盖后保存会把 A 数据写进 B）。
  const detailSeq = useRef(0)
  useEffect(() => {
    const seq = ++detailSeq.current
    if (!id) {
      setName('')
      setDescription('')
      setLanguage('python')
      setContent('')
      setSavedSnap(JSON.stringify({ n: '', d: '', l: 'python', c: '' })) // 离开守卫放行后复位
      return
    }
    loadScripts().catch(() => {})
    get<Script>(`/api/v1/scripts/${id}`)
      .then((s) => {
        if (seq !== detailSeq.current) return
        setName(s.name || '')
        setDescription(s.description || '')
        setLanguage(s.language || 'python')
        setContent(s.content || '')
        setSavedSnap(JSON.stringify({ n: s.name || '', d: s.description || '', l: s.language || 'python', c: s.content || '' }))
      })
      .catch((e) => { if (seq === detailSeq.current) message.error(e.message) })
  }, [id])

  const filtered = useMemo(
    () => scripts.filter((s) => (s.name || '').toLowerCase().includes(search.toLowerCase())),
    [scripts, search],
  )

  const dirty = JSON.stringify({ n: name, d: description, l: language, c: content }) !== savedSnap
  const { guard, allowOnce } = useLeaveGuard(dirty)

  const save = async () => {
    if (!name.trim()) {
      message.error(t('Name is required'))
      return
    }
    if (!content.trim()) {
      message.error(t('Content is required'))
      return
    }
    if (saving) return
    setSaving(true)
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
        message.success(t('Saved'))
        setSavedSnap(JSON.stringify({ n: name.trim(), d: description, l: language.trim() || 'python', c: content }))
        loadScripts()
      } else {
        const r = await post<Script>('/api/v1/scripts', payload)
        message.success(t('Created'))
        allowOnce()
        nav(`/scripts/${r.id}/edit`)
      }
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setSaving(false)
    }
  }

  useSaveShortcut(() => { void save() })

  const create = async () => {
    if (!createName.trim()) {
      message.error(t('Enter a script name'))
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
      message.success(t('Created'))
      allowOnce()
      nav(`/scripts/${r.id}/edit`)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const removeScript = async (s: Script) => {
    try {
      await del(`/api/v1/scripts/${s.id}`)
      message.success(t('Deleted'))
      if (s.id === id) {
        allowOnce()
        nav('/scripts', { replace: true })
      }
      loadScripts().catch((e) => message.error(e.message))
    } catch (e: any) {
      message.error(e.message)
    }
  }

  if (!projectId) return <Card>{t('Select a project at the top first')}</Card>

  const panel = (
    <PanelList
      title={t('Scripts')}
      search={search}
      onSearch={setSearch}
      extra={(
        <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          {t('New')}
        </Button>
      )}
      data={filtered}
      activeId={id}
      onPick={(s) => nav(`/scripts/${s.id}/edit`)}
      renderItem={(s) => (
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
          <span style={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>
            {s.name}
          </span>
          <Space size={4} onClick={(e) => e.stopPropagation()}>
            <Tag style={{ margin: 0 }} color={s.language === 'python' ? 'blue' : 'default'}>
              {s.language || 'python'}
            </Tag>
            <Popconfirm
              title={t('Delete this script?')}
              description={t('This cannot be undone')}
              onConfirm={async () => {
                await removeScript(s)
              }}
            >
              <Button size="small" danger type="text" icon={<DeleteOutlined />} />
            </Popconfirm>
          </Space>
        </div>
      )}
    />
  )

  const toolbar = (
    <Space>
      <Button icon={<ArrowLeftOutlined />} onClick={() => nav('/scripts')}>{t('Back')}</Button>
      <Button type="primary" loading={saving} onClick={save}>{t('Save')}</Button>
      {id && (
        <Typography.Text
          copyable={{ text: id, tooltips: [t('Copy ID'), t('Copied')] }}
          style={{ fontSize: 11, color: PALETTE.textTertiary, whiteSpace: 'nowrap' }}
        >
          ID {id}
        </Typography.Text>
      )}
    </Space>
  )

  const editor = (
    <div style={{ padding: 16, maxWidth: 1000 }}>
      <Field label={t('Name')}>
        <Input
          value={name} onChange={(e) => setName(e.target.value)}
          placeholder={t('Script name')} style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label={t('Description')}>
        <Input
          value={description} onChange={(e) => setDescription(e.target.value)}
          placeholder={t('Script description')} style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label={t('Language')}>
        <Input
          value={language} onChange={(e) => setLanguage(e.target.value)}
          placeholder="python" style={{ width: 160 }}
        />
      </Field>
      <Field label={t('Content')}>
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
      <Empty description={t('Pick a script on the left, or create a new one')} />
      <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>{t('New script')}</Button>
    </div>
  )

  return (
    <>
      <IdeLayout panel={panel} toolbar={editing ? toolbar : undefined}>
        {editing ? editor : placeholder}
      </IdeLayout>
      <Modal
        title={t('New script')}
        open={createOpen}
        okText={t('Create')}
        onCancel={() => setCreateOpen(false)}
        onOk={create}
        destroyOnHidden
      >
        <Input
          value={createName}
          onChange={(e) => setCreateName(e.target.value)}
          onPressEnter={create}
          placeholder={t('Script name')}
        />
      </Modal>
      {guard}
    </>
  )
}
