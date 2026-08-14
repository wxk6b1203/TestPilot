import { Button, Input, Space, Tabs, message } from 'antd'
import { FormatPainterOutlined } from '@ant-design/icons'
import KvEditor from './KvEditor'
import type { Kv } from './KvEditor'
import { PALETTE } from '../theme'

export interface BodyValue { contentType: number; raw?: string; form?: Kv[] }

// 请求体编辑器：无 / JSON / form-data / x-www-form-urlencoded。
export default function BodyEditor({
  value, onChange,
}: {
  value: BodyValue
  onChange: (v: BodyValue) => void
}) {
  const raw = value.raw ?? ''
  const fmt = () => {
    try {
      onChange({ ...value, raw: JSON.stringify(JSON.parse(raw), null, 2) })
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
        onChange={(e) => onChange({ ...value, raw: e.target.value })}
        style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace' }}
        placeholder={'{\n  "name": "neo"\n}'}
      />
    </div>
  )
  const formTab = (ct: number) => (
    <KvEditor
      value={value.form ?? []}
      onChange={(kv) => onChange({ ...value, contentType: ct, form: kv })}
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
        if (k === 'none') onChange({ ...value, contentType: 0 })
        else if (k === 'json') onChange({ ...value, contentType: 4 })
        else if (k === 'form-data') onChange({ ...value, contentType: 2 })
        else onChange({ ...value, contentType: 3 })
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
