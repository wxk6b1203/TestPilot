import { Button, Input, Space, Tabs } from 'antd'
import { FormatPainterOutlined } from '@ant-design/icons'
import { useRef } from 'react'
import KvEditor from './KvEditor'
import type { Kv } from './KvEditor'
import { PALETTE } from '../theme'
import { message } from '../messageBridge'

// form 形状与 proto 对齐（FormData{fields:[…]}）：后端 protojson 直接解析，
// 旧数据里 form 为数组的形态在读取侧兼容（见 fieldsOf）。
export interface FormBody { fields: Kv[] }
export interface BodyValue { contentType: number; raw?: string; form?: FormBody }

// 兼容读取：旧形状 form 是数组，新形状是 {fields:[…]}
const fieldsOf = (f: FormBody | Kv[] | undefined): Kv[] =>
  Array.isArray(f) ? f : f?.fields ?? []

// 请求体编辑器：无 / JSON / form-data / x-www-form-urlencoded。
// BodySpec 的 content 是 oneof（raw / form 互斥），写入时只保留当前 tab 的字段，
// 否则序列化出的 body 同时带 raw+form 会被 protojson 拒绝（后端静默丢弃）。
export default function BodyEditor({
  value, onChange,
}: {
  value: BodyValue
  onChange: (v: BodyValue) => void
}) {
  const raw = value.raw ?? ''
  const fields = fieldsOf(value.form)
  // 两类内容互斥存储，但 UI 侧各留草稿：切 tab 不清另一类，切回即恢复
  const rawDraftRef = useRef(raw)
  rawDraftRef.current = raw || rawDraftRef.current
  const formDraftRef = useRef(fields)
  formDraftRef.current = fields.length ? fields : formDraftRef.current
  const fmt = () => {
    try {
      onChange({ contentType: value.contentType, raw: JSON.stringify(JSON.parse(raw), null, 2) })
    } catch {
      message.error('JSON 格式错误，无法格式化')
    }
  }
  const jsonTab = (
    <div>
      <Space style={{ marginBottom: 6 }}>
        <Button size="small" icon={<FormatPainterOutlined />} onClick={fmt}>格式化</Button>
      </Space>
      <Input.TextArea
        rows={10}
        value={raw}
        onChange={(e) => onChange({ contentType: value.contentType, raw: e.target.value })}
        style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }}
        placeholder={'{\n  "name": "neo"\n}'}
      />
    </div>
  )
  const formTab = (ct: number) => (
    <KvEditor
      value={fields}
      onChange={(kv) => onChange({ contentType: ct, form: { fields: kv } })}
      keyPlaceholder="字段名" valuePlaceholder="字段值"
    />
  )
  const tab = (() => {
    switch (value.contentType) {
      case 4: return 'json'
      case 2: return 'form-data'
      case 3: return 'urlencoded'
      default: return 'none'
    }
  })()
  return (
    <Tabs
      size="small"
      activeKey={tab}
      onChange={(k) => {
        if (k === 'none') onChange({ contentType: 0 })
        else if (k === 'json') onChange({ contentType: 4, raw: rawDraftRef.current })
        else if (k === 'form-data') onChange({ contentType: 2, form: { fields: formDraftRef.current } })
        else onChange({ contentType: 3, form: { fields: formDraftRef.current } })
      }}
      items={[
        { key: 'none', label: '无', children: <span style={{ color: PALETTE.textTertiary }}>无请求体</span> },
        { key: 'json', label: 'JSON', children: jsonTab },
        { key: 'form-data', label: 'form-data', children: formTab(2) },
        { key: 'urlencoded', label: 'x-www-form-urlencoded', children: formTab(3) },
      ]}
    />
  )
}
