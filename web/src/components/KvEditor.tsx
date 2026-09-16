import { Button, Checkbox, Input, Tooltip } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { PALETTE } from '../theme'
import { t } from '../i18n'
import { useStableRows } from '../hooks/useStableRows'
import type { FieldDesign } from '../api'

export interface Kv { key: string; value: string; enabled?: boolean }

// 类型色（与 SchemaTree 同一套语义：string 绿 / 整数浮点 玫红 / bool 蓝 / 其余橙）
const TYPE_COLORS: Record<string, string> = {
  string: '#52c41a', integer: '#eb2f96', number: '#eb2f96', boolean: '#1677ff',
  array: '#722ed1', object: '#fa8c16',
}

const kvEq = (a: Kv, b: Kv) => a.key === b.key && a.value === b.value && a.enabled === b.enabled

// 键值行编辑器（参数/请求头/Cookie/form 字段共用）：key/value 输入 + 删除；"+ 添加" 追加。
// checkable（调试态）：行首勾选框决定该参数是否随请求发送；meta 提供设计态的
// 类型/必填/说明（只读提示，编辑设计请到「设计」页签）。
export default function KvEditor({
  value, onChange, keyPlaceholder = t('Param name'), valuePlaceholder = t('Value (supports {{var}})'),
  checkable = false, meta = {},
}: {
  value: Kv[]
  onChange: (v: Kv[]) => void
  keyPlaceholder?: string
  valuePlaceholder?: string
  checkable?: boolean
  meta?: Record<string, FieldDesign>
}) {
  const { rows, update } = useStableRows(value, kvEq)
  const set = (i: number, patch: Partial<Kv>) => {
    onChange(update(value.map((kv, idx) => (idx === i ? { ...kv, ...patch } : kv))))
  }
  return (
    <div>
      {rows.map((r, i) => {
        const d = meta[r.item.key.trim()]
        const off = checkable && r.item.enabled === false
        const row = (
          <div key={r.id} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6, opacity: off ? 0.45 : 1 }}>
            {checkable && (
              <Checkbox
                checked={r.item.enabled !== false}
                onChange={(e) => set(i, { enabled: e.target.checked })}
              />
            )}
            <Tooltip title={d?.description || r.item.key}>
              <Input
                size="small" style={{ width: 200 }} value={r.item.key}
                placeholder={keyPlaceholder}
                onChange={(e) => set(i, { key: e.target.value })}
              />
            </Tooltip>
            {d && (
              <Tooltip title={`${d.required ? t('Required') + ' · ' : ''}${d.description || d.type || ''}`}>
                <span style={{ fontSize: 11, color: TYPE_COLORS[d.type ?? ''] ?? PALETTE.textTertiary, flexShrink: 0 }}>
                  {d.type || ''}
                  {d.required && <span style={{ color: '#ff4d4f', marginLeft: 2 }}>*</span>}
                </span>
              </Tooltip>
            )}
            {!d && <span style={{ flexShrink: 0, width: 1 }} />}
            <span style={{ color: PALETTE.textTertiary }}>=</span>
            <Input
              size="small" style={{ flex: 1 }} value={r.item.value}
              placeholder={d?.example ? t('e.g. {v}', { v: d.example }) : valuePlaceholder}
              onChange={(e) => set(i, { value: e.target.value })}
            />
            <Button
              type="text" size="small" icon={<DeleteOutlined />}
              style={{ color: PALETTE.textTertiary }}
              onClick={() => onChange(update(value.filter((_, idx) => idx !== i)))}
            />
          </div>
        )
        return row
      })}
      <Button
        type="dashed" size="small" icon={<PlusOutlined />}
        style={{ width: '100%', color: PALETTE.textTertiary }}
        onClick={() => onChange(update([...value, { key: '', value: '', ...(checkable ? { enabled: true } : {}) }]))}
      >
        {t('Add')}
      </Button>
    </div>
  )
}
