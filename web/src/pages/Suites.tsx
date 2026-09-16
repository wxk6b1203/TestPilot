import { Button, Card, Empty, Input, Modal, Space, Tag, Typography } from 'antd'
import {
  ArrowDownOutlined, ArrowLeftOutlined, ArrowUpOutlined, LeftOutlined, PlusOutlined, RightOutlined,
} from '@ant-design/icons'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, put } from '../api'
import type { ListResp, Suite, TestCase } from '../api'
import IdeLayout from '../components/IdeLayout'
import EntityTreePanel from '../components/EntityTreePanel'
import { PALETTE } from '../theme'
import useSaveShortcut from '../hooks/useSaveShortcut'
import { useLeaveGuard } from '../hooks/useLeaveGuard'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'
import { t } from '../i18n'

// getter 每次读取求值：语言切换即时生效
const CASE_TYPE: Record<number, { text: string; color: string }> = {
  get 1() { return { text: t('Declarative'), color: 'blue' } },
  get 2() { return { text: t('Low-code'), color: 'purple' } },
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ fontSize: 12, color: PALETTE.textSecondary, marginBottom: 4 }}>{label}</div>
      {children}
    </div>
  )
}

function CaseLine({ c, index }: { c: TestCase | undefined; index?: number }) {
  const meta = c ? CASE_TYPE[c.type] ?? { text: String(c.type), color: 'default' } : { text: t('Missing'), color: 'default' }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
      {index !== undefined && (
        <span style={{ color: PALETTE.textTertiary, fontSize: 12, width: 18, textAlign: 'right' }}>{index}</span>
      )}
      <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>
        {c?.name ?? t('(case deleted)')}
      </span>
      <Tag style={{ margin: 0 }} color={meta.color}>{meta.text}</Tag>
    </div>
  )
}

interface PickRow { id: string; active: boolean; onClick: () => void; content: ReactNode }

function PickCol({ title, extra, rows }: { title: string; extra?: ReactNode; rows: PickRow[] }) {
  return (
    <div style={{
      flex: 1, minWidth: 0, border: `1px solid ${PALETTE.border}`, borderRadius: 6,
      background: '#FFFFFF', display: 'flex', flexDirection: 'column',
    }}>
      <div style={{
        padding: '6px 10px', borderBottom: `1px solid ${PALETTE.border}`,
        display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8,
      }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: PALETTE.textSecondary }}>{title}</span>
        {extra}
      </div>
      <div style={{ overflow: 'auto', padding: 6, maxHeight: 320, minHeight: 200 }}>
        {rows.map((r) => (
          <div
            key={r.id}
            onClick={r.onClick}
            style={{
              padding: '5px 8px', cursor: 'pointer', borderRadius: 4, marginBottom: 2,
              background: r.active ? PALETTE.selectedRow : 'transparent',
            }}
          >
            {r.content}
          </div>
        ))}
        {rows.length === 0 && (
          <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 24, fontSize: 12 }}>{t('None')}</div>
        )}
      </div>
    </div>
  )
}

export default function Suites() {
  const nav = useNavigate()
  const { id } = useParams()
  const { projectId } = useLayout()
  const [cases, setCases] = useState<TestCase[]>([])
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [leftSel, setLeftSel] = useState('')
  const [rightSel, setRightSel] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [createName, setCreateName] = useState('')
  const [createParentId, setCreateParentId] = useState<string>()
  const [refresh, setRefresh] = useState(0)
  const [saving, setSaving] = useState(false)
  // 已保存快照（dirty 判定 + 离开守卫）
  const [savedSnap, setSavedSnap] = useState(() => JSON.stringify({ n: '', d: '', ids: [] as string[] }))
  const editing = !!id

  useEffect(() => {
    setCases([])
    if (!projectId) return
    get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=500`)
      .then((r) => setCases(r.items))
      .catch((e) => message.error(e.message))
  }, [projectId])

  // 进入/切换编辑路由时加载详情（含有序 case_ids）。seq 防乱序：快速切换
  // A→B 时 A 的慢响应不得覆盖 B 的表单（组件不因 id 重挂载，覆盖后保存会
  // 把 A 的 name/case_ids 写进 B）。
  const detailSeq = useRef(0)
  useEffect(() => {
    const seq = ++detailSeq.current
    if (!id) {
      setName('')
      setDescription('')
      setSelectedIds([])
      setSavedSnap(JSON.stringify({ n: '', d: '', ids: [] as string[] })) // 离开守卫放行后复位
      return
    }
    get<Suite>(`/api/v1/suites/${id}`)
      .then((s) => {
        if (seq !== detailSeq.current) return
        setName(s.name || '')
        setDescription(s.description || '')
        setSelectedIds(s.case_ids || [])
        setSavedSnap(JSON.stringify({ n: s.name || '', d: s.description || '', ids: s.case_ids || [] }))
      })
      .catch((e) => { if (seq === detailSeq.current) message.error(e.message) })
  }, [id])

  const selectedSet = useMemo(() => new Set(selectedIds), [selectedIds])
  const available = cases.filter((c) => !selectedSet.has(c.id))

  const addSel = () => {
    if (!leftSel) return
    setSelectedIds([...selectedIds, leftSel])
    setLeftSel('')
  }
  const removeSel = () => {
    if (!rightSel) return
    setSelectedIds(selectedIds.filter((x) => x !== rightSel))
    setRightSel('')
  }
  const move = (delta: number) => {
    const idx = selectedIds.indexOf(rightSel)
    if (idx < 0) return
    const to = idx + delta
    if (to < 0 || to >= selectedIds.length) return
    const arr = [...selectedIds]
    const tmp = arr[idx]
    arr[idx] = arr[to]
    arr[to] = tmp
    setSelectedIds(arr)
  }

  const dirty = JSON.stringify({ n: name, d: description, ids: selectedIds }) !== savedSnap
  const { guard, allowOnce } = useLeaveGuard(dirty)

  const save = async () => {
    if (!name.trim()) {
      message.error(t('Name is required'))
      return
    }
    if (saving) return
    setSaving(true)
    const payload = { project_id: projectId, name: name.trim(), description, case_ids: selectedIds }
    try {
      if (id) {
        await put(`/api/v1/suites/${id}`, payload)
        message.success(t('Saved'))
        setSavedSnap(JSON.stringify({ n: name.trim(), d: description, ids: selectedIds }))
        setRefresh((x) => x + 1)
      } else {
        const r = await post<Suite>('/api/v1/suites', payload)
        message.success(t('Created'))
        allowOnce()
        nav(`/suites/${r.id}/edit`)
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
      message.error(t('Enter a suite name'))
      return
    }
    try {
      const r = await post<Suite>('/api/v1/suites', {
        project_id: projectId, name: createName.trim(), description: '', case_ids: [],
      })
      if (createParentId) {
        await post('/api/v1/tree/nodes', {
          project_id: projectId, ref_type: 5, ref_id: r.id, parent_id: createParentId,
        })
      }
      setCreateOpen(false)
      setCreateName('')
      setCreateParentId(undefined)
      setRefresh((x) => x + 1)
      message.success(t('Created'))
      allowOnce()
      nav(`/suites/${r.id}/edit`)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const openCreate = (parentId?: string) => {
    setCreateName('')
    setCreateParentId(parentId)
    setCreateOpen(true)
  }

  if (!projectId) return <Card>{t('Select a project at the top first')}</Card>

  const panel = (
    <EntityTreePanel
      title={t('Suites')}
      kind="suite"
      projectId={projectId}
      activeId={id}
      refresh={refresh}
      onPick={(sid) => nav(`/suites/${sid}/edit`)}
      onNewInFolder={openCreate}
      onDeleted={(deletedId) => {
        if (deletedId === id) nav('/suites', { replace: true })
      }}
    />
  )

  const toolbar = (
    <Space>
      <Button icon={<ArrowLeftOutlined />} onClick={() => nav('/suites')}>{t('Back')}</Button>
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
          placeholder={t('Suite name')} style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label={t('Description')}>
        <Input
          value={description} onChange={(e) => setDescription(e.target.value)}
          placeholder={t('Suite description')} style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label={t('Case orchestration (execution order)')}>
        <div style={{ display: 'flex', gap: 12, alignItems: 'stretch' }}>
          <PickCol
            title={t('All cases')}
            rows={available.map((c) => ({
              id: c.id,
              active: leftSel === c.id,
              onClick: () => setLeftSel(c.id),
              content: <CaseLine c={c} />,
            }))}
          />
          <div style={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 8 }}>
            <Button icon={<RightOutlined />} disabled={!leftSel} onClick={addSel}>{t('Add')}</Button>
            <Button icon={<LeftOutlined />} disabled={!rightSel} onClick={removeSel}>{t('Remove')}</Button>
          </div>
          <PickCol
            title={t('Selected ({count})', { count: selectedIds.length })}
            extra={(
              <Space size={0}>
                <Button size="small" type="text" icon={<ArrowUpOutlined />} disabled={!rightSel} onClick={() => move(-1)} />
                <Button size="small" type="text" icon={<ArrowDownOutlined />} disabled={!rightSel} onClick={() => move(1)} />
                <Button size="small" type="text" danger disabled={!rightSel} onClick={removeSel}>{t('Remove')}</Button>
              </Space>
            )}
            rows={selectedIds.map((cid, i) => ({
              id: cid,
              active: rightSel === cid,
              onClick: () => setRightSel(cid),
              content: <CaseLine c={cases.find((x) => x.id === cid)} index={i + 1} />,
            }))}
          />
        </div>
      </Field>
    </div>
  )

  const placeholder = (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 48, gap: 12 }}>
      <Empty description={t('Pick a suite on the left, or create a new one')} />
      <Button type="primary" icon={<PlusOutlined />} onClick={() => openCreate()}>{t('New suite')}</Button>
    </div>
  )

  return (
    <>
      <IdeLayout panel={panel} toolbar={editing ? toolbar : undefined}>
        {editing ? editor : placeholder}
      </IdeLayout>
      <Modal
        title={t('New suite')}
        open={createOpen}
        okText={t('Create')}
        onCancel={() => { setCreateOpen(false); setCreateParentId(undefined) }}
        onOk={create}
        destroyOnHidden
      >
        <Input
          value={createName}
          onChange={(e) => setCreateName(e.target.value)}
          onPressEnter={create}
          placeholder={t('Suite name')}
        />
      </Modal>
      {guard}
    </>
  )
}
