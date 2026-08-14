import { Button, Card, Empty, Input, Modal, Space, Tag, message } from 'antd'
import {
  ArrowDownOutlined, ArrowLeftOutlined, ArrowUpOutlined, LeftOutlined, PlusOutlined, RightOutlined,
} from '@ant-design/icons'
import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, put } from '../api'
import type { ListResp, Suite, TestCase } from '../api'
import IdeLayout from '../components/IdeLayout'
import PanelList from '../components/PanelList'
import { PALETTE } from '../theme'
import { useLayout } from './Layout'

const CASE_TYPE: Record<number, { text: string; color: string }> = {
  1: { text: '声明式', color: 'blue' },
  2: { text: '低代码', color: 'purple' },
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
  const meta = c ? CASE_TYPE[c.type] ?? { text: String(c.type), color: 'default' } : { text: '缺失', color: 'default' }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
      {index !== undefined && (
        <span style={{ color: PALETTE.textTertiary, fontSize: 12, width: 18, textAlign: 'right' }}>{index}</span>
      )}
      <span style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 13 }}>
        {c?.name ?? '（用例已删除）'}
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
          <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 24, fontSize: 12 }}>暂无</div>
        )}
      </div>
    </div>
  )
}

export default function Suites() {
  const nav = useNavigate()
  const { id } = useParams()
  const { projectId } = useLayout()
  const [suites, setSuites] = useState<Suite[]>([])
  const [cases, setCases] = useState<TestCase[]>([])
  const [search, setSearch] = useState('')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [selectedIds, setSelectedIds] = useState<string[]>([])
  const [leftSel, setLeftSel] = useState('')
  const [rightSel, setRightSel] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [createName, setCreateName] = useState('')
  const editing = !!id

  const loadSuites = () =>
    projectId
      ? get<ListResp<Suite>>(`/api/v1/suites?project_id=${projectId}&page_size=200`).then((r) => setSuites(r.items))
      : Promise.resolve()

  useEffect(() => {
    setSuites([])
    setCases([])
    if (!projectId) return
    loadSuites().catch((e) => message.error(e.message))
    get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=200`)
      .then((r) => setCases(r.items))
      .catch((e) => message.error(e.message))
  }, [projectId])

  // 进入/切换编辑路由时加载详情（含有序 case_ids）
  useEffect(() => {
    if (!id) {
      setName('')
      setDescription('')
      setSelectedIds([])
      return
    }
    loadSuites().catch(() => {})
    get<Suite>(`/api/v1/suites/${id}`)
      .then((s) => {
        setName(s.name || '')
        setDescription(s.description || '')
        setSelectedIds(s.case_ids || [])
      })
      .catch((e) => message.error(e.message))
  }, [id])

  const filtered = useMemo(
    () => suites.filter((s) => (s.name || '').toLowerCase().includes(search.toLowerCase())),
    [suites, search],
  )
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

  const save = async () => {
    if (!name.trim()) {
      message.error('名称必填')
      return
    }
    const payload = { project_id: projectId, name: name.trim(), description, case_ids: selectedIds }
    try {
      if (id) {
        await put(`/api/v1/suites/${id}`, payload)
        message.success('已保存')
        loadSuites()
      } else {
        const r = await post<Suite>('/api/v1/suites', payload)
        message.success('已创建')
        nav(`/suites/${r.id}/edit`)
      }
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const create = async () => {
    if (!createName.trim()) {
      message.error('请输入套件名称')
      return
    }
    try {
      const r = await post<Suite>('/api/v1/suites', {
        project_id: projectId, name: createName.trim(), description: '', case_ids: [],
      })
      setCreateOpen(false)
      setCreateName('')
      message.success('已创建')
      nav(`/suites/${r.id}/edit`)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  const panel = (
    <PanelList
      title="套件"
      search={search}
      onSearch={setSearch}
      extra={(
        <Button size="small" type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>
          新建
        </Button>
      )}
      data={filtered}
      activeId={id}
      onPick={(s) => nav(`/suites/${s.id}/edit`)}
      renderItem={(s) => (
        <div>
          <div style={{ color: PALETTE.text, fontWeight: 500, fontSize: 13 }}>{s.name}</div>
          {s.description && <div style={{ color: PALETTE.textTertiary, fontSize: 12 }}>{s.description}</div>}
        </div>
      )}
    />
  )

  const toolbar = (
    <Space>
      <Button icon={<ArrowLeftOutlined />} onClick={() => nav('/suites')}>返回</Button>
      <Button type="primary" onClick={save}>保存</Button>
    </Space>
  )

  const editor = (
    <div style={{ padding: 16, maxWidth: 1000 }}>
      <Field label="名称">
        <Input
          value={name} onChange={(e) => setName(e.target.value)}
          placeholder="套件名称" style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label="描述">
        <Input
          value={description} onChange={(e) => setDescription(e.target.value)}
          placeholder="套件描述" style={{ maxWidth: 480 }}
        />
      </Field>
      <Field label="用例编排（按执行序）">
        <div style={{ display: 'flex', gap: 12, alignItems: 'stretch' }}>
          <PickCol
            title="全部用例"
            rows={available.map((c) => ({
              id: c.id,
              active: leftSel === c.id,
              onClick: () => setLeftSel(c.id),
              content: <CaseLine c={c} />,
            }))}
          />
          <div style={{ display: 'flex', flexDirection: 'column', justifyContent: 'center', gap: 8 }}>
            <Button icon={<RightOutlined />} disabled={!leftSel} onClick={addSel}>加入</Button>
            <Button icon={<LeftOutlined />} disabled={!rightSel} onClick={removeSel}>移出</Button>
          </div>
          <PickCol
            title={`已选（${selectedIds.length}）`}
            extra={(
              <Space size={0}>
                <Button size="small" type="text" icon={<ArrowUpOutlined />} disabled={!rightSel} onClick={() => move(-1)} />
                <Button size="small" type="text" icon={<ArrowDownOutlined />} disabled={!rightSel} onClick={() => move(1)} />
                <Button size="small" type="text" danger disabled={!rightSel} onClick={removeSel}>移除</Button>
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
      <Empty description="从左侧选择套件，或新建一个套件" />
      <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>新建套件</Button>
    </div>
  )

  return (
    <>
      <IdeLayout panel={panel} toolbar={editing ? toolbar : undefined}>
        {editing ? editor : placeholder}
      </IdeLayout>
      <Modal
        title="新建套件"
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
          placeholder="套件名称"
        />
      </Modal>
    </>
  )
}
