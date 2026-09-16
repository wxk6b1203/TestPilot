// JSON Schema 工具库：数据模型（结构）与接口设计共用的类型层。
// Schema 采用 draft-07 子集：type/properties/items/required/description/default/enum/format。
// type 取值固定为 SCHEMA_TYPES（「any」非严格 JSON Schema 类型，作为未知/混合类型的兜底）。

export const SCHEMA_TYPES = ['string', 'integer', 'number', 'boolean', 'array', 'object', 'any'] as const
export type SchemaType = (typeof SCHEMA_TYPES)[number]

export type JsonSchema = {
  type?: string
  title?: string
  description?: string
  required?: string[]
  default?: any
  enum?: any[]
  format?: string
  properties?: Record<string, JsonSchema>
  items?: JsonSchema
  [k: string]: any
}

// 设计态预设行：params/headers/cookies 设计共用（HttpApi.params_design 等）。
// default/example 用字符串承载（输入框直接编辑），发起请求时按目标类型转换。
export interface FieldDesign {
  name: string
  type: string
  required?: boolean
  default?: string
  description?: string
  example?: string
}

export const FIELD_TYPES = ['string', 'integer', 'number', 'boolean', 'array', 'object'] as const

// ---- JSON → Schema 推断（「通过 JSON 生成」）----

export function jsonToSchema(v: unknown): JsonSchema {
  if (v === null || v === undefined) return { type: 'any' }
  switch (typeof v) {
    case 'string':
      return { type: 'string' }
    case 'boolean':
      return { type: 'boolean' }
    case 'number':
      return Number.isInteger(v) ? { type: 'integer' } : { type: 'number' }
  }
  if (Array.isArray(v)) {
    return { type: 'array', items: v.length ? mergeSchemas(v.map(jsonToSchema)) : { type: 'any' } }
  }
  const o = v as Record<string, unknown>
  const properties: Record<string, JsonSchema> = {}
  for (const [k, val] of Object.entries(o)) properties[k] = jsonToSchema(val)
  return { type: 'object', properties }
}

// 多个子 schema 归并：同类型标量取该类型；object 深合并 properties；类型不一致兜底 any。
function mergeSchemas(list: JsonSchema[]): JsonSchema {
  if (list.length === 0) return { type: 'any' }
  const first = list[0]
  if (list.every((s) => s.type === 'object' && first.type === 'object')) {
    const properties: Record<string, JsonSchema> = { ...(first.properties ?? {}) }
    for (const s of list.slice(1)) {
      for (const [k, v] of Object.entries(s.properties ?? {})) {
        properties[k] = properties[k] ? mergeSchemas([properties[k], v]) : v
      }
    }
    return { type: 'object', properties }
  }
  return list.every((s) => s.type === first.type) ? first : { type: 'any' }
}

// ---- Schema → 示例 JSON（调试时按结构生成默认请求体）----

export function schemaToExample(s: JsonSchema | undefined | null): unknown {
  if (!s) return undefined
  if (s.enum?.length) return s.enum[0]
  if (s.default !== undefined) return typedDefault(s.default, s.type)
  switch (s.type) {
    case 'string':
      return 'string'
    case 'integer':
    case 'number':
      return 0
    case 'boolean':
      return true
    case 'array':
      return [schemaToExample(s.items ?? { type: 'any' })].filter((x) => x !== undefined)
    case 'object': {
      const out: Record<string, unknown> = {}
      for (const [k, v] of Object.entries(s.properties ?? {})) out[k] = schemaToExample(v)
      return out
    }
    default:
      return null // any / 未标注
  }
}

// default 在 Schema 里以字符串输入为主，写出示例时按目标类型还原（失败保持原值）。
export function typedDefault(raw: unknown, type?: string): unknown {
  if (typeof raw !== 'string' || !type) return raw
  switch (type) {
    case 'integer':
    case 'number': {
      const n = Number(raw)
      return Number.isNaN(n) ? raw : n
    }
    case 'boolean':
      return raw === 'true' ? true : raw === 'false' ? false : raw
    case 'object':
    case 'array':
      try {
        return JSON.parse(raw)
      } catch {
        return raw
      }
    default:
      return raw
  }
}

// ---- Schema ↔ 行树（SchemaTree 编辑器模型）----
// 行树把 properties/items 摊平成可编辑行：array 的 items 显示为锁定的 ITEMS 子行，
// object 的每个 property 是一行；root 行对应 schema 本身（名称不可改，展示「根节点」）。

export interface SchemaRow {
  key: string
  name: string // 字段名；root 行为空（展示「根节点」）、ITEMS 行锁定
  locked?: boolean // root / ITEMS 行：不可改名、不可删除
  type: string
  description: string
  defaultValue: string
  required: boolean
  children?: SchemaRow[]
}

let rowSeq = 0
const nextKey = () => `r${++rowSeq}`

export function schemaToRows(s: JsonSchema): SchemaRow {
  const row: SchemaRow = {
    key: nextKey(),
    name: '',
    locked: true,
    type: s.type ?? 'object',
    description: s.description ?? '',
    defaultValue: s.default !== undefined ? JSON.stringify(s.default) : '',
    required: false,
  }
  row.children = childRows(s)
  return row
}

function childRows(s: JsonSchema): SchemaRow[] {
  if (s.type === 'object') {
    const req = new Set(s.required ?? [])
    return Object.entries(s.properties ?? {}).map(([name, sub]) => {
      const r: SchemaRow = {
        key: nextKey(),
        name,
        type: sub.type ?? 'any',
        description: sub.description ?? '',
        defaultValue: sub.default !== undefined ? JSON.stringify(sub.default) : '',
        required: req.has(name),
      }
      r.children = childRows(sub)
      return r
    })
  }
  if (s.type === 'array') {
    const items = s.items ?? { type: 'any' }
    const r: SchemaRow = {
      key: nextKey(),
      name: 'ITEMS',
      locked: true,
      type: items.type ?? 'any',
      description: items.description ?? '',
      defaultValue: '',
      required: false,
    }
    r.children = childRows(items)
    return [r]
  }
  return []
}

export function rowsToSchema(root: SchemaRow): JsonSchema {
  const s: JsonSchema = { type: root.type }
  if (root.description) s.description = root.description
  if (root.defaultValue) {
    try {
      s.default = JSON.parse(root.defaultValue)
    } catch {
      s.default = root.defaultValue
    }
  }
  applyChildren(s, root.children ?? [])
  return s
}

function applyChildren(s: JsonSchema, rows: SchemaRow[]) {
  if (s.type === 'object') {
    const properties: Record<string, JsonSchema> = {}
    const required: string[] = []
    for (const r of rows) {
      if (!r.name) continue
      properties[r.name] = rowToSchema(r)
      if (r.required) required.push(r.name)
    }
    if (Object.keys(properties).length) s.properties = properties
    if (required.length) s.required = required
    return
  }
  if (s.type === 'array') {
    s.items = rows.length ? rowToSchema(rows[0]) : { type: 'any' }
  }
}

function rowToSchema(r: SchemaRow): JsonSchema {
  const s: JsonSchema = { type: r.type }
  if (r.description) s.description = r.description
  if (r.defaultValue) {
    try {
      s.default = JSON.parse(r.defaultValue)
    } catch {
      s.default = r.defaultValue
    }
  }
  applyChildren(s, r.children ?? [])
  return s
}
