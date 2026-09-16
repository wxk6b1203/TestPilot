import { Button, Card, Collapse, Input, InputNumber, Popconfirm, Select, Space, Tabs, Typography } from 'antd'
import { DeleteOutlined, PlusOutlined, SaveOutlined, SearchOutlined } from '@ant-design/icons'
import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { del, get, post, put } from '../api'
import useSaveShortcut from '../hooks/useSaveShortcut'
import { useLeaveGuard } from '../hooks/useLeaveGuard'
import type { GrpcApi, ListResp, ProtoFile } from '../api'
import IdeLayout from '../components/IdeLayout'
import KvEditor from '../components/KvEditor'
import type { Kv } from '../components/KvEditor'
import { PALETTE } from '../theme'
import { useLayout } from '../hooks/useLayout'
import { message } from '../messageBridge'
import { t } from '../i18n'

type TabKey = 'grpc' | 'proto'

interface GrpcDraft {
  protoRef?: string
  address: string
  fullService: string
  method: string
  requestText: string
  metadata: Kv[]
  deadlineMs: number | null
  tlsText: string
}
const EMPTY_GRPC: GrpcDraft = {
  address: '', fullService: '', method: '', requestText: '', metadata: [], deadlineMs: null, tlsText: '',
}

interface ProtoDraft { filename: string; content: string }
const EMPTY_PROTO: ProtoDraft = { filename: '', content: '' }

// 解析可选 JSON 字段：空文本 → undefined；非法 JSON 抛错（由调用方 catch 提示）。
function parseJson(text: string, label: string): any {
  if (!text.trim()) return undefined
  try {
    return JSON.parse(text)
  } catch (e: any) {
    throw new Error(t('{label}: invalid JSON — {msg}', { label, msg: e.message }))
  }
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ fontSize: 12, color: PALETTE.textSecondary, marginBottom: 4 }}>{label}</div>
      {children}
    </div>
  )
}

function PaneRow({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <div
      onClick={onClick}
      style={{
        padding: '7px 12px', cursor: 'pointer', borderRadius: 6, margin: '2px 0',
        background: active ? PALETTE.selectedRow : 'transparent',
        color: PALETTE.text,
      }}
    >
      {children}
    </div>
  )
}

export default function GrpcApis() {
  const { projectId, envId, envs } = useLayout()
  const [grpcApis, setGrpcApis] = useState<GrpcApi[]>([])
  const [protoFiles, setProtoFiles] = useState<ProtoFile[]>([])
  const [searchGrpc, setSearchGrpc] = useState('')
  const [searchProto, setSearchProto] = useState('')
  const [activeTab, setActiveTab] = useState<TabKey>('grpc')
  const [selectedGrpcId, setSelectedGrpcId] = useState<string | undefined>()
  const [selectedProtoId, setSelectedProtoId] = useState<string | undefined>()
  const [draft, setDraft] = useState<GrpcDraft>({ ...EMPTY_GRPC })
  const [draftProto, setDraftProto] = useState<ProtoDraft>({ ...EMPTY_PROTO })
  // 已保存快照（dirty 判定 + 离开守卫）：gRPC 与 Proto 两张表单合一
  const [savedGrpc, setSavedGrpc] = useState(() => JSON.stringify(EMPTY_GRPC))
  const [savedProto, setSavedProto] = useState(() => JSON.stringify(EMPTY_PROTO))
  const dirty = JSON.stringify(draft) !== savedGrpc || JSON.stringify(draftProto) !== savedProto
  const { guard } = useLeaveGuard(dirty)

  const loadGrpc = () =>
    projectId
      ? get<ListResp<GrpcApi>>(`/api/v1/grpc-apis?project_id=${projectId}&page_size=500`).then((r) => { setGrpcApis(r.items) })
      : Promise.resolve()
  const loadProto = () =>
    projectId
      ? get<ListResp<ProtoFile>>(`/api/v1/proto-files?project_id=${projectId}&page_size=500`).then((r) => { setProtoFiles(r.items) })
      : Promise.resolve()

  useEffect(() => {
    setGrpcApis([])
    setProtoFiles([])
    if (!projectId) return
    loadGrpc().catch((e) => message.error(e.message))
    loadProto().catch((e) => message.error(e.message))
  }, [projectId])

  const grpcFiltered = useMemo(
    () => grpcApis.filter((g) => `${g.full_service}.${g.method}`.toLowerCase().includes(searchGrpc.toLowerCase())),
    [grpcApis, searchGrpc],
  )
  const protoFiltered = useMemo(
    () => protoFiles.filter((p) => (p.filename || '').toLowerCase().includes(searchProto.toLowerCase())),
    [protoFiles, searchProto],
  )
  const envBase = envs.find((e) => e.id === envId)?.base_url

  const pickGrpc = (g: GrpcApi) => {
    setSelectedGrpcId(g.id)
    const d: GrpcDraft = {
      protoRef: g.proto_ref || undefined,
      address: g.address || '',
      fullService: g.full_service || '',
      method: g.method || '',
      requestText: g.request_message ? JSON.stringify(g.request_message, null, 2) : '',
      metadata: (g.metadata || []).map((m) => ({ key: m.key, value: m.value })),
      deadlineMs: g.deadline_ms ?? null,
      tlsText: g.tls_settings ? JSON.stringify(g.tls_settings, null, 2) : '',
    }
    setDraft(d)
    setActiveTab('grpc')
    setSavedGrpc(JSON.stringify(d))
  }
  const newGrpc = () => {
    setSelectedGrpcId(undefined)
    setDraft({ ...EMPTY_GRPC })
    setActiveTab('grpc')
  }
  const saveGrpc = async () => {
    if (!draft.fullService.trim() || !draft.method.trim()) {
      message.error(t('full_service and method are required'))
      return
    }
    try {
      const request_message = parseJson(draft.requestText, 'request_message')
      const tls_settings = parseJson(draft.tlsText, 'tls_settings')
      const payload: Record<string, unknown> = {
        project_id: projectId,
        address: draft.address.trim(),
        full_service: draft.fullService.trim(),
        method: draft.method.trim(),
        metadata: draft.metadata.filter((m) => m.key.trim()),
      }
      if (draft.protoRef) payload.proto_ref = draft.protoRef
      if (draft.deadlineMs != null) payload.deadline_ms = draft.deadlineMs
      if (request_message !== undefined) payload.request_message = request_message
      if (tls_settings !== undefined) payload.tls_settings = tls_settings
      if (selectedGrpcId) {
        await put(`/api/v1/grpc-apis/${selectedGrpcId}`, payload)
        message.success(t('Saved'))
        setSavedGrpc(JSON.stringify(draft))
      } else {
        const r = await post<GrpcApi>('/api/v1/grpc-apis', payload)
        setSelectedGrpcId(r.id)
        message.success(t('Created'))
        setSavedGrpc(JSON.stringify(draft))
      }
      loadGrpc()
    } catch (e: any) {
      message.error(e.message)
    }
  }
  const removeGrpc = async () => {
    if (!selectedGrpcId) return
    try {
      await del(`/api/v1/grpc-apis/${selectedGrpcId}`)
      message.success(t('Deleted'))
      setSelectedGrpcId(undefined)
      setDraft({ ...EMPTY_GRPC })
      setSavedGrpc(JSON.stringify(EMPTY_GRPC))
      loadGrpc()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const pickProto = (pf: ProtoFile) => {
    setSelectedProtoId(pf.id)
    const d: ProtoDraft = { filename: pf.filename || '', content: pf.content || '' }
    setDraftProto(d)
    setActiveTab('proto')
    setSavedProto(JSON.stringify(d))
  }
  const newProto = () => {
    setSelectedProtoId(undefined)
    setDraftProto({ ...EMPTY_PROTO })
    setActiveTab('proto')
  }
  const saveProto = async () => {
    if (!draftProto.filename.trim() || !draftProto.content.trim()) {
      message.error(t('filename and content are required'))
      return
    }
    const payload = {
      project_id: projectId,
      filename: draftProto.filename.trim(),
      content: draftProto.content,
    }
    try {
      if (selectedProtoId) {
        await put(`/api/v1/proto-files/${selectedProtoId}`, payload)
        message.success(t('Saved'))
        setSavedProto(JSON.stringify(draftProto))
      } else {
        const r = await post<ProtoFile>('/api/v1/proto-files', payload)
        setSelectedProtoId(r.id)
        message.success(t('Created'))
        setSavedProto(JSON.stringify(draftProto))
      }
      loadProto()
    } catch (e: any) {
      message.error(e.message)
    }
  }
  useSaveShortcut(() => { void (activeTab === 'grpc' ? saveGrpc() : saveProto()) })
  const removeProto = async () => {
    if (!selectedProtoId) return
    try {
      await del(`/api/v1/proto-files/${selectedProtoId}`)
      message.success(t('Deleted'))
      setSelectedProtoId(undefined)
      setDraftProto({ ...EMPTY_PROTO })
      setSavedProto(JSON.stringify(EMPTY_PROTO))
      loadProto()
    } catch (e: any) {
      message.error(e.message)
    }
  }

  if (!projectId) return <Card>{t('Select a project at the top first')}</Card>

  const panel = (
    <div style={{ height: '100%', overflow: 'auto', padding: '6px 8px' }}>
      <Collapse
        ghost
        defaultActiveKey={['grpc', 'proto']}
        items={[
          {
            key: 'grpc',
            label: <span style={{ fontWeight: 600, fontSize: 13 }}>{t('gRPC APIs ({count})', { count: grpcApis.length })}</span>,
            children: (
              <div>
                <Input
                  size="small" allowClear
                  prefix={<SearchOutlined style={{ color: PALETTE.textTertiary }} />}
                  placeholder={t('Search…')} value={searchGrpc}
                  onChange={(e) => setSearchGrpc(e.target.value)}
                />
                <div style={{ marginTop: 6 }}>
                  {grpcFiltered.map((g) => (
                    <PaneRow key={g.id} active={g.id === selectedGrpcId} onClick={() => pickGrpc(g)}>
                      <div style={{ fontFamily: 'monospace', fontSize: 12 }}>{g.full_service}.{g.method}</div>
                      <div style={{ fontSize: 11, color: PALETTE.textTertiary, marginTop: 2 }}>
                        {g.address || t('use environment base_url')}
                      </div>
                    </PaneRow>
                  ))}
                  {grpcFiltered.length === 0 && (
                    <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 24, fontSize: 12 }}>{t('No data')}</div>
                  )}
                </div>
              </div>
            ),
          },
          {
            key: 'proto',
            label: <span style={{ fontWeight: 600, fontSize: 13 }}>{t('Proto files ({count})', { count: protoFiles.length })}</span>,
            children: (
              <div>
                <Input
                  size="small" allowClear
                  prefix={<SearchOutlined style={{ color: PALETTE.textTertiary }} />}
                  placeholder={t('Search…')} value={searchProto}
                  onChange={(e) => setSearchProto(e.target.value)}
                />
                <div style={{ marginTop: 6 }}>
                  {protoFiltered.map((p) => (
                    <PaneRow key={p.id} active={p.id === selectedProtoId} onClick={() => pickProto(p)}>
                      <span style={{ fontSize: 12, fontFamily: 'monospace' }}>{p.filename}</span>
                    </PaneRow>
                  ))}
                  {protoFiltered.length === 0 && (
                    <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 24, fontSize: 12 }}>{t('No data')}</div>
                  )}
                </div>
              </div>
            ),
          },
        ]}
      />
    </div>
  )

  const grpcTab = (
    <div>
      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        padding: '10px 16px', borderBottom: `1px solid ${PALETTE.border}`,
      }}>
        <Typography.Text strong style={{ fontSize: 14 }}>
          {selectedGrpcId ? t('Edit gRPC API') : t('New gRPC API')}
        </Typography.Text>
        <Space>
          <Button size="small" icon={<PlusOutlined />} onClick={newGrpc}>{t('New')}</Button>
          <Button size="small" type="primary" icon={<SaveOutlined />} onClick={saveGrpc}>{t('Save')}</Button>
          <Popconfirm title={t('Delete this gRPC API?')} onConfirm={removeGrpc} disabled={!selectedGrpcId}>
            <Button size="small" danger icon={<DeleteOutlined />} disabled={!selectedGrpcId}>{t('Delete')}</Button>
          </Popconfirm>
        </Space>
      </div>
      <div style={{ padding: 16, maxWidth: 920 }}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 16px' }}>
          <Field label={t('proto_ref (optional, link to a Proto file)')}>
            <Select
              allowClear
              placeholder={t('Not linked')}
              style={{ width: '100%' }}
              value={draft.protoRef}
              onChange={(v) => setDraft({ ...draft, protoRef: v })}
              options={protoFiles.map((p) => ({ value: p.id, label: p.filename }))}
            />
          </Field>
          <Field label={t('address (host:port; empty = environment base_url)')}>
            <Input
              placeholder="host:port"
              value={draft.address}
              onChange={(e) => setDraft({ ...draft, address: e.target.value })}
            />
          </Field>
          <Field label="full_service">
            <Input
              placeholder="pkg.Service"
              value={draft.fullService}
              onChange={(e) => setDraft({ ...draft, fullService: e.target.value })}
            />
          </Field>
          <Field label="method">
            <Input
              placeholder="SayHello"
              value={draft.method}
              onChange={(e) => setDraft({ ...draft, method: e.target.value })}
            />
          </Field>
        </div>
        <Field label={t('deadline_ms (ms, optional)')}>
          <InputNumber
            style={{ width: 240 }} min={0} placeholder={t('No limit')}
            value={draft.deadlineMs}
            onChange={(v) => setDraft({ ...draft, deadlineMs: v })}
          />
        </Field>
        <Field label={t('request_message (JSON)')}>
          <Input.TextArea
            rows={6}
            style={{ fontFamily: 'monospace', fontSize: 12 }}
            placeholder={'{"name": "neo"}'}
            value={draft.requestText}
            onChange={(e) => setDraft({ ...draft, requestText: e.target.value })}
          />
        </Field>
        <Field label="metadata">
          <KvEditor
            value={draft.metadata}
            onChange={(v) => setDraft({ ...draft, metadata: v })}
            keyPlaceholder="key" valuePlaceholder="value"
          />
        </Field>
        <Field label={t('tls_settings (JSON, optional)')}>
          <Input.TextArea
            rows={4}
            style={{ fontFamily: 'monospace', fontSize: 12 }}
            placeholder={'{"insecure": true}'}
            value={draft.tlsText}
            onChange={(e) => setDraft({ ...draft, tlsText: e.target.value })}
          />
        </Field>
        {envBase && (
          <div style={{ fontSize: 12, color: PALETTE.textTertiary }}>
            {t('Environment base_url: {url} (used when address is empty)', { url: envBase })}
          </div>
        )}
      </div>
    </div>
  )

  const protoTab = (
    <div>
      <div style={{
        display: 'flex', justifyContent: 'space-between', alignItems: 'center',
        padding: '10px 16px', borderBottom: `1px solid ${PALETTE.border}`,
      }}>
        <Typography.Text strong style={{ fontSize: 14 }}>
          {selectedProtoId ? t('Edit Proto file') : t('New Proto file')}
        </Typography.Text>
        <Space>
          <Button size="small" icon={<PlusOutlined />} onClick={newProto}>{t('New')}</Button>
          <Button size="small" type="primary" icon={<SaveOutlined />} onClick={saveProto}>{t('Save')}</Button>
          <Popconfirm title={t('Delete this Proto file?')} onConfirm={removeProto} disabled={!selectedProtoId}>
            <Button size="small" danger icon={<DeleteOutlined />} disabled={!selectedProtoId}>{t('Delete')}</Button>
          </Popconfirm>
        </Space>
      </div>
      <div style={{ padding: 16, maxWidth: 920 }}>
        <Field label="filename">
          <Input
            placeholder="service.proto"
            value={draftProto.filename}
            onChange={(e) => setDraftProto({ ...draftProto, filename: e.target.value })}
          />
        </Field>
        <Field label="content">
          <Input.TextArea
            rows={18}
            style={{ fontFamily: 'monospace', fontSize: 12 }}
            placeholder={'syntax = "proto3";\n\nservice Greeter {\n  rpc SayHello (HelloRequest) returns (HelloReply);\n}'}
            value={draftProto.content}
            onChange={(e) => setDraftProto({ ...draftProto, content: e.target.value })}
          />
        </Field>
      </div>
    </div>
  )

  return (
    <IdeLayout panel={panel}>
      <Tabs
        tabBarStyle={{ padding: '0 8px' }}
        activeKey={activeTab}
        onChange={(k) => setActiveTab(k as TabKey)}
        items={[
          { key: 'grpc', label: t('gRPC editor'), children: grpcTab },
          { key: 'proto', label: t('Proto editor'), children: protoTab },
        ]}
      />
      {guard}
    </IdeLayout>
  )
}
