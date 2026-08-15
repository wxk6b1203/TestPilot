import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { PALETTE } from '../theme'

// 可拖拽分栏（参考 file-browser SplitPane 的 pointer 实现，React 版精简）：
// direction: horizontal=左右分栏（竖向分隔条，拖拽改宽）；vertical=上下分栏（横向分隔条，拖拽改高）。
// 首栏尺寸 initial 支持 px 数字或 'xx%'（相对容器）；min/max 同理；双击分隔条复位到 initial。
export default function SplitPane({
  direction, initial = 300, min = 100, max = 0, onResize, onResizeEnd, children,
}: {
  direction: 'horizontal' | 'vertical'
  initial?: number | string
  min?: number | string
  max?: number | string
  onResize?: (size: number) => void
  onResizeEnd?: (size: number) => void
  children: [ReactNode, ReactNode]
}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState<number | null>(null)
  const [active, setActive] = useState(false)
  const dragging = useRef(false)
  const customized = useRef(false) // 拖拽/双击复位后为 true：容器 resize 只 clamp，不再重排 initial

  const parse = useCallback((v: number | string, total: number): number => {
    if (typeof v === 'number') return v
    if (typeof v === 'string' && v.endsWith('%')) return (total * parseFloat(v)) / 100
    return parseFloat(String(v)) || 0
  }, [])

  const initialPx = useCallback(() => {
    const el = containerRef.current
    if (!el) return typeof initial === 'number' ? initial : 0
    const total = direction === 'horizontal' ? el.clientWidth : el.clientHeight
    return parse(initial, total)
  }, [direction, initial, parse])

  // 首次布局用 initial 计算（未拖拽前 size=null 表示用 initial）
  useEffect(() => {
    setSize(initialPx())
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [direction, initial])

  // 容器尺寸变化（窗口缩放、面板重排）：未定制则跟随 initial，已定制则夹回 min/max
  useEffect(() => {
    const el = containerRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => {
      setSize(customized.current ? (v: number | null) => (v == null ? v : clamp(v)) : initialPx())
    })
    ro.observe(el)
    return () => ro.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [direction, initial, min, max])

  const clamp = (v: number) => {
    const el = containerRef.current
    const total = el ? (direction === 'horizontal' ? el.clientWidth : el.clientHeight) : 0
    if (!total) return v
    const mn = parse(min, total)
    const mx = max ? parse(max, total) : total - 40
    return Math.max(mn, Math.min(v, mx || total - 40))
  }

  const onPointerDown = (e: React.PointerEvent) => {
    dragging.current = true
    customized.current = true
    setActive(true)
    e.currentTarget.setPointerCapture(e.pointerId)
  }
  const onPointerMove = (e: React.PointerEvent) => {
    if (!dragging.current || !containerRef.current) return
    const rect = containerRef.current.getBoundingClientRect()
    const v = direction === 'horizontal' ? e.clientX - rect.left : e.clientY - rect.top
    setSize(clamp(v))
    onResize?.(clamp(v))
  }
  const onPointerUp = (e: React.PointerEvent) => {
    if (!dragging.current) return
    dragging.current = false
    setActive(false)
    e.currentTarget.releasePointerCapture(e.pointerId)
    const el = containerRef.current
    if (el) {
      const rect = el.getBoundingClientRect()
      const v = direction === 'horizontal' ? e.clientX - rect.left : e.clientY - rect.top
      const s = clamp(v)
      setSize(s)
      onResizeEnd?.(s)
    }
  }
  const onDoubleClick = () => {
    setSize(initialPx())
    onResizeEnd?.(initialPx())
  }

  const firstStyle: React.CSSProperties =
    direction === 'horizontal'
      ? { flex: `0 0 ${size ?? initial}px`, minWidth: 0, overflow: 'auto' }
      : { flex: `0 0 ${size ?? initial}px`, minHeight: 0, overflow: 'auto' }

  const dividerStyle: React.CSSProperties = direction === 'horizontal'
    ? { flex: '0 0 5px', cursor: 'col-resize', margin: '0 -2px', zIndex: 1 }
    : { flex: '0 0 5px', cursor: 'row-resize', margin: '-2px 0', zIndex: 1 }

  return (
    <div
      ref={containerRef}
      style={{
        display: 'flex',
        flexDirection: direction === 'horizontal' ? 'row' : 'column',
        height: '100%',
        width: '100%',
        minWidth: 0,
        minHeight: 0,
      }}
    >
      <div style={firstStyle}>{children[0]}</div>
      <div
        style={dividerStyle}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
        onDoubleClick={onDoubleClick}
      >
        <div
          style={{
            width: direction === 'horizontal' ? 1 : '100%',
            height: direction === 'horizontal' ? '100%' : 1,
            margin: direction === 'horizontal' ? '0 auto' : 'auto 0',
            background: active ? PALETTE.primary : PALETTE.border,
          }}
        />
      </div>
      <div style={{ flex: 1, minWidth: 0, minHeight: 0, overflow: 'auto' }}>{children[1]}</div>
    </div>
  )
}
