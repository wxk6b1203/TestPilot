import { Button, Dropdown, Input, Modal, Select, Space, Tabs } from 'antd'
import { CloudUploadOutlined, DatabaseOutlined, EyeOutlined, StopOutlined } from '@ant-design/icons'
import { useEffect, useState } from 'react'
import { get } from '../api'
import type { DataModel, FieldDesign, HttpApi } from '../api'
import DesignKvTable from './DesignKvTable'
import SchemaTree, { ensureEditable } from './SchemaTree'
import { jsonToSchema, schemaToExample } from '../lib/jsonSchema'
import type { JsonSchema } from '../lib/jsonSchema'
import { PALETTE } from '../theme'
import { message } from '../messageBridge'

// 接口「设计」页签：对 Params / Body / Headers / Cookies / 响应 做预设——
// 类型、必填、默认值、说明（存 HttpApi.*_design / request_schema / response_schema）。
// 「调试」页签按这些预设生成参数、回填默认值（勾选是否发送）。
export interface ApiDesign {
  params: FieldDesign[]
  headers: FieldDesign[]
  cookies: FieldDesign[]
  requestSchema: JsonSchema | null
  responseSchema: JsonSchema | null
}

export const EMPTY_DESIGN: ApiDesign = {
  params: [], headers: [], cookies: [], requestSchema: null, responseSchema: null,
}

export default function ApiDesignPanel({ design, onChange }: {
  design: ApiDesign
  onChange: (d: ApiDesign) => void
}) {
  const [tab, setTab] = useState('params')
  const [jsonModal, setJsonModal] = useState<'request' | 'response'>()
  const [jsonText, setJsonText] = useState('')
  const [schemaModal, setSchemaModal] = useState<'request' | 'response'>()
  const [schemaText, setSchemaText] = useState('')
  const [modelPick, setModelPick] = useState<Record<string, DataModel[]>>({})
  const [pickedModel, setPickedModel] = useState('')
  // 外部整体替换结构（导入/引用模型/移除）时递增，驱动 SchemaTree 重建行树；
  // 树内编辑走 onChange 回写、不递增——否则每敲一个字都重建行会丢输入焦点。
  const [schemaEpoch, setSchemaEpoch] = useState(0)
  const bumpEpoch = () => setSchemaEpoch((n) => n + 1)

  // 数据模型列表（「结构」）：Body/响应结构可一键引用
  const loadModels = async (which: 'request' | 'response') => {
    try {
      const r = await get<{ items: DataModel[] }>('/api/v1/models?page_size=500')
      setModelPick((prev) => ({ ...prev, [which]: r.items }))
      setPickedModel('')
    } catch (e: any) {
      message.error(e.message)
    }
  }

  useEffect(() => {
    if ((jsonModal || schemaModal) && !modelPick.request) void loadModels('request')
  }, [jsonModal, schemaModal]) // eslint-disable-line react-hooks/exhaustive-deps

  const applyParsed = (which: 'request' | 'response', s: JsonSchema) => {
    onChange({ ...design, [which === 'request' ? 'requestSchema' : 'responseSchema']: ensureEditable(s) })
    setJsonModal(undefined)
    setSchemaModal(undefined)
    setJsonText('')
    setSchemaText('')
    bumpEpoch()
  }

  const applyFromJson = () => {
    try {
      const v = JSON.parse(jsonText.trim())
      applyParsed(jsonModal!, jsonToSchema(v))
    } catch {
      message.error('JSON 解析失败，请检查格式')
    }
  }

  const applyFromSchema = () => {
    try {
      const v = JSON.parse(schemaText.trim())
      if (!v || typeof v !== 'object' || !v.type) {
        message.error('不是合法的 JSON Schema（缺少 type 字段）')
        return
      }
      applyParsed(schemaModal!, v)
    } catch {
      message.error('JSON 解析失败，请检查格式')
    }
  }

  const applyFromModel = (which: 'request' | 'response') => {
    const m = (modelPick[which] ?? []).find((x) => x.id === pickedModel)
    if (!m) {
      message.warning('请选择数据模型')
      return
    }
    applyParsed(which, m.schema ?? { type: 'object', properties: {} })
  }

  const schemaBlock = (which: 'request' | 'response') => {
    const key = which === 'request' ? 'requestSchema' : 'responseSchema'
    const s = design[key]
    const models = modelPick[which] ?? []
    if (!s) {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12, alignItems: 'flex-start', padding: '8px 0' }}>
          <Space wrap>
            <Dropdown
              menu={{
                items: [
                  { key: 'json', label: '通过 JSON 生成' },
                  { key: 'schema', label: '通过 JSON Schema 生成' },
                ],
                onClick: ({ key }) => {
                  setJsonText('')
                  setSchemaText('')
                  if (key === 'json') setJsonModal(which)
                  else setSchemaModal(which)
                },
              }}
            >
              <Button size="small" icon={<CloudUploadOutlined />}>生成结构</Button>
            </Dropdown>
            <Select
              size="small" style={{ minWidth: 220 }} placeholder="引用数据模型（结构）"
              value={pickedModel || undefined}
              showSearch optionFilterProp="label"
              onDropdownVisibleChange={(open) => open && !models.length && loadModels(which)}
              options={models.map((m) => ({ value: m.id, label: m.name }))}
              onChange={setPickedModel}
            />
            <Button size="small" icon={<DatabaseOutlined />} onClick={() => applyFromModel(which)}>应用模型</Button>
          </Space>
          <span style={{ fontSize: 12, color: PALETTE.textTertiary }}>
            未设计结构时调试可自由编辑请求体；设计后调试会按结构生成示例并回填默认值。
          </span>
        </div>
      )
    }
    return (
      <div>
        <Space style={{ marginBottom: 8 }} wrap>
          <Button
            size="small" danger type="text" icon={<StopOutlined />}
            onClick={() => { onChange({ ...design, [key]: null }); bumpEpoch() }}
          >
            移除结构
          </Button>
          <Button
            size="small" type="text" icon={<EyeOutlined />}
            onClick={() => {
              Modal.info({
                title: '按结构生成的示例 JSON',
                width: 640,
                content: (
                  <Input.TextArea
                    rows={14} readOnly
                    value={JSON.stringify(schemaToExample(s) ?? {}, null, 2)}
                    style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12, marginTop: 12 }}
                  />
                ),
                okText: '关闭',
              })
            }}
          >
            预览示例
          </Button>
        </Space>
        <SchemaTree
          schema={ensureEditable(s)}
          onChange={(next) => onChange({ ...design, [key]: next })}
          resetKey={`${which}-${schemaEpoch}`}
        />
      </div>
    )
  }

  const designTabs = [
    { key: 'params', label: 'Params', children: (
      <DesignKvTable value={design.params} onChange={(v) => onChange({ ...design, params: v })} />
    ) },
    { key: 'body', label: 'Body 结构', children: schemaBlock('request') },
    { key: 'headers', label: 'Headers', children: (
      <DesignKvTable value={design.headers} onChange={(v) => onChange({ ...design, headers: v })} nameHeader="Header 名" />
    ) },
    { key: 'cookies', label: 'Cookies', children: (
      <DesignKvTable value={design.cookies} onChange={(v) => onChange({ ...design, cookies: v })} nameHeader="Cookie 名" />
    ) },
    { key: 'response', label: '响应结构', children: schemaBlock('response') },
  ]

  const which = jsonModal ?? schemaModal
  return (
    <div style={{ height: '100%', overflow: 'auto', padding: '8px 16px' }}>
      <Tabs size="small" activeKey={tab} onChange={setTab} items={designTabs} />
      {/* 通过 JSON / JSON Schema 生成请求或响应结构 */}
      <Modal
        title={`通过 ${which && jsonModal ? 'JSON' : 'JSON Schema'} 生成${which === 'request' ? '请求' : '响应'}结构`}
        open={!!which}
        onCancel={() => { setJsonModal(undefined); setSchemaModal(undefined) }}
        onOk={jsonModal ? applyFromJson : applyFromSchema}
        okText="生成"
        width={640}
        destroyOnHidden
      >
        <Input.TextArea
          rows={14}
          value={jsonModal ? jsonText : schemaText}
          onChange={(e) => (jsonModal ? setJsonText : setSchemaText)(e.target.value)}
          placeholder={jsonModal
            ? '{\n  "code": 0,\n  "msg": "ok"\n}'
            : '{\n  "type": "object",\n  "properties": { "code": { "type": "integer" } }\n}'}
          style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 12 }}
        />
      </Modal>
    </div>
  )
}

// design → 调试参数的元信息映射（KvEditor 提示用）
export function designMeta(rows: FieldDesign[] | undefined): Record<string, FieldDesign> {
  return Object.fromEntries((rows ?? []).filter((d) => d.name).map((d) => [d.name, d]))
}

// HttpApi 响应 → 设计态（后端 JSON 列直接是对象/数组）
export function designOf(api: HttpApi): ApiDesign {
  return {
    params: api.params_design ?? [],
    headers: api.headers_design ?? [],
    cookies: api.cookies_design ?? [],
    requestSchema: api.request_schema ?? null,
    responseSchema: api.response_schema ?? null,
  }
}
