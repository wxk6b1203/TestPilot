import { useEffect, useRef } from 'react'

// Cmd/Ctrl+S 触发保存（编辑器页通用）。用 latest-ref 避免闭包捕获过期回调。
export default function useSaveShortcut(onSave: () => void, enabled = true) {
  const ref = useRef(onSave)
  ref.current = onSave
  useEffect(() => {
    if (!enabled) return
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault() // 阻止浏览器"保存网页"
        ref.current()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [enabled])
}
