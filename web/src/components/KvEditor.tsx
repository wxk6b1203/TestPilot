import { Button, Input } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { PALETTE } from '../theme'

export interface Kv { key: string; value: string }

// 键值行编辑器（参数/请求头共用）：key/value 输入 + 删除；"+ 添加" 追加。
export default function KvEditor({
  value, onChange, keyPlaceholder = '参数名', valuePlaceholder = '参数值（支持 {{var}}）',
}: {
  value: Kv[]
  onChange: (v: Kv[]) => void
  keyPlaceholder?: string
  valuePlaceholder?: string
}) {
  const set = (i: number, patch: Partial<Kv>) => {
    const next = value.map((kv, idx) => (idx === i ? { ...kv, ...patch } : kv))
    onChange(next)
  }
  return (
    <div>
      {value.map((kv, i) => (
        <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 6 }}>
          <Input
            size="small" style={{ width: 200 }} value={kv.key}
            placeholder={keyPlaceholder}
            onChange={(e) => set(i, { key: e.target.value })}
          />
          <span style={{ color: PALETTE.textTertiary }}>=</span>
          <Input
            size="small" style={{ flex: 1 }} value={kv.value}
            placeholder={valuePlaceholder}
            onChange={(e) => set(i, { value: e.target.value })}
          />
          <Button
            type="text" size="small" icon={<DeleteOutlined />}
            style={{ color: PALETTE.textTertiary }}
            onClick={() => onChange(value.filter((_, idx) => idx !== i))}
          />
        </div>
      ))}
      <Button
        type="dashed" size="small" icon={<PlusOutlined />}
        style={{ width: '100%', color: PALETTE.textTertiary }}
        onClick={() => onChange([...value, { key: '', value: '' }])}
      >
        添加
      </Button>
    </div>
  )
}
