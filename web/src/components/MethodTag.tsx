import { METHOD_COLORS } from '../theme'

// 方法语义标签（GET 绿 / POST 橙 …），列表与调试区共用。
export default function MethodTag({ method, size = 12 }: { method: number; size?: number }) {
  const m = METHOD_COLORS[method] ?? METHOD_COLORS[7]
  return (
    <span
      style={{
        display: 'inline-block',
        minWidth: size === 12 ? 52 : 64,
        textAlign: 'center',
        fontWeight: 700,
        fontStyle: 'italic',
        fontSize: size,
        color: m.color,
        background: m.bg,
        borderRadius: 4,
        padding: '2px 6px',
        lineHeight: '18px',
      }}
    >
      {m.text}
    </span>
  )
}
