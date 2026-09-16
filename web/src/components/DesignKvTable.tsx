import { Button, Checkbox, Input, Select } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { PALETTE } from '../theme'
import { t } from '../i18n'
import { FIELD_TYPES } from '../lib/jsonSchema'
import type { FieldDesign } from '../api'

const TYPE_COLORS: Record<string, string> = {
  string: '#52c41a', integer: '#eb2f96', number: '#eb2f96', boolean: '#1677ff',
  array: '#722ed1', object: '#fa8c16',
}

const typeOptions = FIELD_TYPES.map((t) => ({
  value: t,
  label: <span style={{ color: TYPE_COLORS[t] }}>{t}</span>,
}))

// 设计态键值预设表格（Params / Headers / Cookies 共用）：
// 标记类型、必填、默认值、说明——调试页签按此生成参数并回填默认值（勾选发送）。
// 空列表也始终渲染一行空行供直接输入；空行直接保留在 value 里（保存方负责
// 过滤无名行——ApiDebug payload 的 prune），否则「添加」会被过滤逻辑吞掉。
const EMPTY_FIELD: FieldDesign = { name: '', type: 'string' }

export default function DesignKvTable({ value, onChange, nameHeader = t('Param name') }: {
  value: FieldDesign[]
  onChange: (v: FieldDesign[]) => void
  nameHeader?: string
}) {
  const rows = value.length ? value : [EMPTY_FIELD]
  const set = (i: number, patch: Partial<FieldDesign>) =>
    onChange(rows.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  const head = { fontSize: 11, color: PALETTE.textTertiary, fontWeight: 500 } as const
  return (
    <div>
      <div style={{ display: 'flex', gap: 8, marginBottom: 4 }}>
        <span style={{ ...head, width: 200 }}>{nameHeader}</span>
        <span style={{ ...head, width: 110 }}>{t('Type')}</span>
        <span style={{ ...head, width: 50 }}>{t('Required')}</span>
        <span style={{ ...head, flex: 1 }}>{t('Default')}</span>
        <span style={{ ...head, flex: 1 }}>{t('Example')}</span>
        <span style={{ ...head, flex: 1 }}>{t('Description')}</span>
        <span style={{ width: 32 }} />
      </div>
      {rows.map((r, i) => (
        <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
          <Input
            size="small" style={{ width: 200 }} value={r.name} placeholder={nameHeader}
            onChange={(e) => set(i, { name: e.target.value })}
          />
          <Select
            size="small" style={{ width: 110 }} value={r.type || 'string'}
            options={typeOptions} onChange={(v) => set(i, { type: v })}
          />
          <Checkbox
            style={{ width: 50, display: 'flex', justifyContent: 'center' }}
            checked={!!r.required}
            onChange={(e) => set(i, { required: e.target.checked })}
          />
          <Input
            size="small" style={{ flex: 1 }} value={r.default ?? ''} placeholder={t('Default')}
            onChange={(e) => set(i, { default: e.target.value })}
          />
          <Input
            size="small" style={{ flex: 1 }} value={r.example ?? ''} placeholder={t('Example')}
            onChange={(e) => set(i, { example: e.target.value })}
          />
          <Input
            size="small" style={{ flex: 1 }} value={r.description ?? ''} placeholder={t('Description')}
            onChange={(e) => set(i, { description: e.target.value })}
          />
          <Button
            type="text" size="small" icon={<DeleteOutlined />}
            style={{ color: PALETTE.textTertiary, width: 32 }}
            onClick={() => onChange(rows.filter((_, idx) => idx !== i))}
          />
        </div>
      ))}
      <Button
        type="dashed" size="small" icon={<PlusOutlined />}
        style={{ width: '100%', color: PALETTE.textTertiary }}
        onClick={() => onChange([...value, { name: '', type: 'string' }])}
      >
        {t('Add')}
      </Button>
    </div>
  )
}
