import { Button, Dropdown, Input, Modal, Tag, Typography } from 'antd'
import {
  CloudUploadOutlined, CodeOutlined, DownloadOutlined,
  EyeOutlined, SaveOutlined,
} from '@ant-design/icons'
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { get, post, put } from '../api'
import type { DataModel } from '../api'
import SchemaTree, { ensureEditable } from '../components/SchemaTree'
import { jsonToSchema, schemaToExample } from '../lib/jsonSchema'
import type { JsonSchema } from '../lib/jsonSchema'
import { PALETTE } from '../theme'
import { message } from '../messageBridge'
import useSaveShortcut from '../hooks/useSaveShortcut'

// 数据模型（结构）编辑页：JSON Schema 形态的结构定义。
// 支持通过 JSON / JSON Schema 生成（同 Apifox），字段树编辑，示例预览。
export default function ModelDetail({ newMode, createParentId, onSaved }: {
  newMode?: boolean
  createParentId?: string
  onSaved?: () => void
}) {
  const nav = useNavigate()
  const { id } = useParams()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [schema, setSchema] = useState<JsonSchema>({ type: 'object', properties: {} })
  const [savedId, setSavedId] = useState('')
  const [importModal, setImportModal] = useState<'json' | 'schema'>()
  const [importText, setImportText] = useState('')
  const [previewOpen, setPreviewOpen] = useState(false)
  const [schemaRawOpen, setSchemaRawOpen] = useState(false)
  const [schemaRaw, setSchemaRaw] = useState('')
  const [resetKey, setResetKey] = useState('init')
  const [savedSnapshot, setSavedSnapshot] = useState(() =>
    JSON.stringify({ name: '', description: '', schema: { type: 'object', properties: {} } }))

  const snapshot = () => JSON.stringify({ name, description, schema })
  const dirty = snapshot() !== savedSnapshot

  useEffect(() => {
    if (!id || newMode) return
    get<DataModel>(`/api/v1/models/${id}`)
      .then((m) => {
        setName(m.name ?? '')
        setDescription(m.description ?? '')
        const s = ensureEditable(m.schema && typeof m.schema === 'object' ? m.schema : { type: 'object', properties: {} })
        setSchema(s)
        setSavedId(String(m.id))
        setSavedSnapshot(JSON.stringify({ name: m.name ?? '', description: m.description ?? '', schema: s }))
        // 异步加载完成后必须换 resetKey 重建 SchemaTree 行树：
        // SchemaTree 以行树为编辑态，仅 resetKey 变化时才从 schema 重建（否则空根上编辑会丢已存字段）
        setResetKey(`loaded-${m.id}`)
      })
      .catch((e) => message.error(e.message))
  }, [id, newMode])

  useSaveShortcut(save)

  const applySchema = (s: JsonSchema) => {
    setSchema(ensureEditable(s))
    setResetKey((k) => `${k}+`)
  }

  const doImport = () => {
    const text = importText.trim()
    if (!text) return
    try {
      const parsed = JSON.parse(text)
      if (importModal === 'json') {
        applySchema(jsonToSchema(parsed))
      } else {
        if (parsed && typeof parsed === 'object' && !parsed.type) {
          message.error('不是合法的 JSON Schema（缺少 type 字段）')
          return
        }
        applySchema(parsed)
      }
      setImportModal(undefined)
      setImportText('')
    } catch {
      message.error('JSON 解析失败，请检查格式')
    }
  }

  const payload = () => ({
    name: name.trim(),
    description,
    schema,
  })

  async function save() {
    try {
      if (!name.trim() && !savedId) {
        message.warning('请输入结构名称')
        return
      }
      if (savedId) {
        await put<DataModel>(`/api/v1/models/${savedId}`, payload())
        setSavedSnapshot(snapshot())
        message.success('已保存')
        onSaved?.()
      } else {
        const r = await post<DataModel>('/api/v1/models', {
          ...payload(),
          parent_node_id: createParentId || undefined,
        })
        message.success('已保存')
        setSavedSnapshot(snapshot())
        onSaved?.()
        nav(`/models/${r.id}`, { replace: true })
      }
    } catch (e: any) {
      message.error(e.message)
    }
  }

  const openSchemaRaw = () => {
    setSchemaRaw(JSON.stringify(schema, null, 2))
    setSchemaRawOpen(true)
  }
  const applySchemaRaw = () => {
    try {
      const parsed = JSON.parse(schemaRaw)
      applySchema(parsed)
      setSchemaRawOpen(false)
      message.success('已应用')
    } catch {
      message.error('JSON 解析失败，请检查格式')
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: '#FFFFFF' }}>
      {/* 名称/描述/操作（删除入口在左侧树节点右键菜单，工作区不放删除按钮） */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '8px 12px',
        borderBottom: `1px solid ${PALETTE.border}`, flexShrink: 0,
      }}>
        <Input
          size="small" style={{ width: 260 }} value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="结构名称，如 Response / PageRequest"
        />
        <Input
          size="small" style={{ flex: 1 }} value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="说明（可空）"
        />
        {dirty && <Tag style={{ marginInlineEnd: 0 }} color="warning">未保存</Tag>}
        {savedId && (
          <Typography.Text
            copyable={{ text: savedId, tooltips: ['复制 ID', '已复制'] }}
            style={{ fontSize: 11, color: PALETTE.textTertiary, whiteSpace: 'nowrap' }}
          >
            ID {savedId}
          </Typography.Text>
        )}
        <Button icon={<SaveOutlined />} type="primary" onClick={save}>保存</Button>
      </div>

      {/* 工具栏：生成/导入/预览 */}
      <div style={{
        display: 'flex', alignItems: 'center', gap: 8, padding: '6px 12px',
        borderBottom: `1px solid ${PALETTE.border}`, flexShrink: 0,
      }}>
        <Dropdown
          menu={{
            items: [
              { key: 'json', label: '通过 JSON 生成' },
              { key: 'schema', label: '通过 JSON Schema 生成' },
            ],
            onClick: ({ key }) => {
              setImportText('')
              setImportModal(key as 'json' | 'schema')
            },
          }}
        >
          <Button size="small" icon={<CloudUploadOutlined />}>生成结构</Button>
        </Dropdown>
        <Button size="small" icon={<CodeOutlined />} onClick={openSchemaRaw}>JSON Schema</Button>
        <Button
          size="small" icon={<EyeOutlined />}
          onClick={() => setPreviewOpen(true)}
        >
          预览示例
        </Button>
        <span style={{ flex: 1 }} />
        <span style={{ fontSize: 11, color: PALETTE.textTertiary }}>
          根节点类型：
          <span style={{ color: PALETTE.primary, fontWeight: 600 }}>{schema.type || 'any'}</span>
        </span>
      </div>

      {/* 字段树 */}
      <div style={{ flex: 1, minHeight: 0, overflow: 'auto', padding: '10px 16px' }}>
        <SchemaTree schema={schema} onChange={setSchema} resetKey={resetKey} />
      </div>

      {/* 通过 JSON / JSON Schema 生成 */}
      <Modal
        title={importModal === 'json' ? '通过 JSON 生成结构' : '通过 JSON Schema 生成结构'}
        open={!!importModal}
        onCancel={() => setImportModal(undefined)}
        onOk={doImport}
        okText="生成"
        width={640}
        destroyOnHidden
      >
        <Input.TextArea
          rows={14}
          value={importText}
          onChange={(e) => setImportText(e.target.value)}
          placeholder={importModal === 'json'
            ? '{\n  "code": 0,\n  "msg": "ok",\n  "data": { "id": 1, "name": "neo" }\n}'
            : '{\n  "type": "object",\n  "properties": { "code": { "type": "integer" } }\n}'}
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
        />
      </Modal>

      {/* JSON Schema 原文查看/编辑 */}
      <Modal
        title="JSON Schema"
        open={schemaRawOpen}
        onCancel={() => setSchemaRawOpen(false)}
        onOk={applySchemaRaw}
        okText="应用"
        width={640}
        destroyOnHidden
      >
        <Input.TextArea
          rows={16}
          value={schemaRaw}
          onChange={(e) => setSchemaRaw(e.target.value)}
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
        />
      </Modal>

      {/* 示例预览 */}
      <Modal
        title="按结构生成的示例 JSON"
        open={previewOpen}
        onCancel={() => setPreviewOpen(false)}
        footer={null}
        width={640}
        destroyOnHidden
      >
        <Input.TextArea
          rows={16}
          readOnly
          value={JSON.stringify(schemaToExample(schema) ?? {}, null, 2)}
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
        />
        <Typography.Link
          style={{ fontSize: 12, marginTop: 8, display: 'inline-flex', alignItems: 'center', gap: 4 }}
          onClick={() => {
            navigator.clipboard?.writeText(JSON.stringify(schemaToExample(schema) ?? {}, null, 2))
            message.success('已复制')
          }}
        >
          <DownloadOutlined /> 复制示例
        </Typography.Link>
      </Modal>
    </div>
  )
}
