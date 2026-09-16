import { useEffect, useMemo, useState } from 'react'
import { Button, Input, Select, Tooltip } from 'antd'
import {
  CaretDownOutlined, CaretRightOutlined, MinusOutlined, PlusOutlined,
} from '@ant-design/icons'
import { PALETTE } from '../theme'
import {
  SCHEMA_TYPES, rowsToSchema, schemaToRows,
} from '../lib/jsonSchema'
import type { JsonSchema, SchemaRow } from '../lib/jsonSchema'

// 类型显示色（与 KvEditor 元信息同一套语义）
const TYPE_COLORS: Record<string, string> = {
  string: '#52c41a', integer: '#eb2f96', number: '#eb2f96', boolean: '#1677ff',
  array: '#722ed1', object: '#fa8c16', any: '#8c8c8c',
}

const typeOptions = SCHEMA_TYPES.map((t) => ({
  value: t,
  label: <span style={{ color: TYPE_COLORS[t] }}>{t}</span>,
}))

// 字段树编辑器（数据模型 / 接口请求响应结构共用）：
// 内部以行树（SchemaRow）为编辑态，每次编辑回写 JSON Schema 给 onChange；
// 外部整体换 schema（导入 JSON / 切换模型）时递增 resetKey 重建行树，
// 其余时候不回灌——否则每次按键都重建行会丢输入焦点。
export default function SchemaTree({ schema, onChange, resetKey = '' }: {
  schema: JsonSchema
  onChange: (s: JsonSchema) => void
  resetKey?: string
}) {
  const [root, setRoot] = useState<SchemaRow>(() => schemaToRows(schema))
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => {
    const r = schemaToRows(schema)
    setRoot(r)
    // 默认全展开
    const all = new Set<string>()
    const walk = (row: SchemaRow) => {
      all.add(row.key)
      ;(row.children ?? []).forEach(walk)
    }
    walk(r)
    setExpanded(all)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey])

  const write = (next: SchemaRow) => {
    setRoot(next)
    onChange(rowsToSchema(next))
  }

  // ---- 行操作（不可变更新：按 key 路径替换）----
  const mapRows = (rows: SchemaRow[], fn: (r: SchemaRow) => SchemaRow | null): SchemaRow[] =>
    rows
      .map((r) => {
        const hit = fn(r)
        if (hit) return hit
        const kids = r.children ? mapRows(r.children, fn) : undefined
        return kids === r.children ? r : { ...r, children: kids }
      })
      .filter((r): r is SchemaRow => r !== null)

  const patch = (key: string, p: Partial<SchemaRow>) =>
    write(mapRows([root], (r) => (r.key === key ? { ...r, ...p } : null))[0])

  // 类型切换：改为 array 时自动补 ITEMS 元素行（否则结构无处可编辑）；
  // 从 array 改走时清掉遗留的 ITEMS 行（避免残留脏结构）。
  const changeType = (key: string, t: string) =>
    write(mapRows([root], (r) => {
      if (r.key !== key) return null
      let children = r.children ?? []
      if (t === 'array' && !children.some((c) => c.locked && c.name === 'ITEMS')) {
        children = [...children, { key: `n${Date.now()}-items`, name: 'ITEMS', locked: true, type: 'string', description: '', defaultValue: '', required: false, children: [] }]
      }
      if (t !== 'array' && r.type === 'array') {
        children = children.filter((c) => !(c.locked && c.name === 'ITEMS'))
      }
      return { ...r, type: t, children }
    })[0])

  const addChild = (parentKey: string, child: SchemaRow) => {
    const next = mapRows([root], (r) =>
      r.key === parentKey ? { ...r, children: [...(r.children ?? []), child] } : r)[0]
    write(next)
    setExpanded((prev) => new Set(prev).add(parentKey))
  }

  const removeRow = (key: string) => {
    const strip = (rows: SchemaRow[]): SchemaRow[] =>
      rows
        .filter((r) => r.key !== key)
        .map((r) => (r.children ? { ...r, children: strip(r.children) } : r))
    write(strip([root])[0])
  }

  const newRow = (name = '', type = 'string'): SchemaRow => ({
    key: `n${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
    name, type, description: '', defaultValue: '', required: false, children: [],
  })

  // 展开后的可见行（含层级）
  const visible = useMemo(() => {
    const out: { row: SchemaRow; depth: number; parentType: string }[] = []
    const walk = (rows: SchemaRow[], depth: number, parentType: string) => {
      for (const r of rows) {
        out.push({ row: r, depth, parentType })
        if (r.children?.length && expanded.has(r.key)) walk(r.children, depth + 1, r.type)
      }
    }
    walk([root], 0, '')
    return out
  }, [root, expanded])

  const toggle = (key: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  const cell = { fontSize: 12 }
  const headStyle = { fontSize: 11, color: PALETTE.textTertiary, fontWeight: 500 } as const

  return (
    <div>
      {/* 列头 */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 4, paddingLeft: 4 }}>
        <span style={{ ...headStyle, width: 220 }}>字段名</span>
        <span style={{ ...headStyle, width: 120 }}>类型</span>
        <span style={{ ...headStyle, width: 60 }}>必填</span>
        <span style={{ ...headStyle, flex: 1 }}>默认值</span>
        <span style={{ ...headStyle, flex: 1 }}>说明</span>
        <span style={{ width: 64 }} />
      </div>
      {visible.map(({ row, depth, parentType }) => {
        const kids = row.children ?? []
        const isItems = row.locked && row.name === 'ITEMS'
        const addItem = () => addChild(row.key, newRow())
        return (
          <div
            key={row.key}
            style={{
              display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4,
              paddingLeft: depth * 16 + 4,
              background: row.locked ? 'rgba(0,0,0,0.02)' : undefined,
              borderRadius: 6, paddingBlock: 2,
            }}
          >
            {/* 展开/字段名 */}
            <span style={{ width: 220, display: 'flex', alignItems: 'center', gap: 4, minWidth: 0 }}>
              {kids.length > 0 ? (
                <Button
                  type="text" size="small" icon={expanded.has(row.key) ? <CaretDownOutlined /> : <CaretRightOutlined />}
                  style={{ width: 20, height: 20, minWidth: 0, padding: 0, color: PALETTE.textTertiary }}
                  onClick={() => toggle(row.key)}
                />
              ) : (
                <span style={{ width: 20, flexShrink: 0 }} />
              )}
              {row.locked ? (
                <span
                  style={{
                    ...cell, color: isItems ? PALETTE.primary : PALETTE.text,
                    background: 'rgba(77,110,235,0.08)',
                    padding: '1px 8px', borderRadius: 4, whiteSpace: 'nowrap',
                  }}
                >
                  {row.name || '根节点'}
                </span>
              ) : (
                <Input
                  size="small" value={row.name} placeholder="字段名"
                  onChange={(e) => patch(row.key, { name: e.target.value })}
                />
              )}
            </span>
            {/* 类型 */}
            <Select
              size="small" value={row.type} options={typeOptions}
              style={{ width: 120 }} onChange={(v) => changeType(row.key, v)}
            />
            {/* 必填（root/ITEMS 无意义） */}
            <span style={{ width: 60, textAlign: 'center' }}>
              {!row.locked && (
                <Tooltip title="必填">
                  <Button
                    size="small" type="text"
                    style={{ padding: 0, width: 24, color: row.required ? '#ff4d4f' : PALETTE.textTertiary, fontWeight: 700 }}
                    onClick={() => patch(row.key, { required: !row.required })}
                  >
                    {row.required ? '*' : '○'}
                  </Button>
                </Tooltip>
              )}
            </span>
            {/* 默认值（root 的默认值意义有限，隐藏） */}
            <span style={{ flex: 1, minWidth: 0 }}>
              {!row.locked && (
                <Input
                  size="small" value={row.defaultValue} placeholder="默认值"
                  onChange={(e) => patch(row.key, { defaultValue: e.target.value })}
                />
              )}
            </span>
            {/* 说明 */}
            <span style={{ flex: 1, minWidth: 0 }}>
              <Input
                size="small" value={row.description} placeholder="说明"
                onChange={(e) => patch(row.key, { description: e.target.value })}
              />
            </span>
            {/* 操作 */}
            <span style={{ width: 64, display: 'flex', justifyContent: 'flex-end', gap: 2 }}>
              {row.type === 'object' && (
                <Button
                  type="text" size="small" icon={<PlusOutlined />}
                  style={{ color: PALETTE.textTertiary }}
                  title="添加子字段"
                  onClick={addItem}
                />
              )}
              {row.type === 'array' && (isItems ? (
                <Button
                  type="text" size="small" icon={<PlusOutlined />}
                  style={{ color: PALETTE.textTertiary }} title="定义元素结构"
                  onClick={() => addChild(row.key, newRow())}
                />
              ) : null)}
              {/* array 行本身添加子行 = items；仅 ITEMS 行可编辑元素结构 */}
              {!row.locked && parentType !== 'array' && (
                <Button
                  type="text" size="small" icon={<MinusOutlined />}
                  style={{ color: PALETTE.textTertiary }} title="删除字段"
                  onClick={() => removeRow(row.key)}
                />
              )}
            </span>
          </div>
        )
      })}
      <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
        {root.type === 'object' && (
          <Button
            type="dashed" size="small" icon={<PlusOutlined />}
            style={{ color: PALETTE.textTertiary }}
            onClick={() => addChild(root.key, newRow())}
          >
            添加字段
          </Button>
        )}
        <span style={{ fontSize: 11, color: PALETTE.textTertiary, alignSelf: 'center' }}>
          object 类型可添加子字段；array 通过 ITEMS 行定义元素结构
        </span>
      </div>
    </div>
  )
}

// 便捷包装：array 根的 ITEMS 缺失时先补一行，便于直接编辑元素结构。
export function ensureEditable(schema: JsonSchema): JsonSchema {
  if (schema.type === 'array' && !schema.items) return { ...schema, items: { type: 'string' } }
  return schema
}
