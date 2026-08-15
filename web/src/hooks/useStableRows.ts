import { useRef } from 'react'

let seq = 0

// 为受控行数组提供稳定行身份：行数据本身不能带 id（payload 直接序列化给后端，
// protojson 拒绝未知字段），用内部包装携带 key，避免索引 key 在删行/编辑时错位。
// 写路径经 update()（按位置沿用旧 id，编辑行不重挂载、焦点不丢）；
// 外部整体替换 value（回填、structuredClone）时按位置+值匹配尽量沿用旧 id。
export function useStableRows<T>(value: T[], eq: (a: T, b: T) => boolean) {
  const innerRef = useRef<{ id: number; item: T }[] | null>(null)
  let rows = innerRef.current
  if (!rows || rows.length !== value.length || rows.some((r, i) => !eq(r.item, value[i]))) {
    const prev = rows
    const used = new Set<number>()
    rows = value.map((item, i) => {
      if (prev && prev[i] && eq(prev[i].item, item) && !used.has(prev[i].id)) {
        used.add(prev[i].id)
        return { id: prev[i].id, item }
      }
      if (prev) {
        const hit = prev.find((r) => eq(r.item, item) && !used.has(r.id))
        if (hit) {
          used.add(hit.id)
          return { id: hit.id, item }
        }
      }
      return { id: ++seq, item }
    })
    innerRef.current = rows
  }
  // 写路径：先更新内部行身份再原样返回（供 onChange 直接使用）
  const update = (next: T[]): T[] => {
    innerRef.current = next.map((item, i) =>
      i < rows.length ? { id: rows[i].id, item } : { id: ++seq, item },
    )
    return next
  }
  return { rows, update }
}
