import { useEffect, useRef } from 'react'
import hotkeys from 'hotkeys-js'

// 项目内快捷键均为修饰键组合（mod+s / mod+enter），允许在输入框/文本域中触发。
// hotkeys-js 默认会忽略 INPUT/TEXTAREA/SELECT，这里全局放开。
hotkeys.filter = () => true

// 统一快捷键 hook（底层使用 hotkeys-js）。
// combo 支持：mod+s / mod+enter / ctrl+alt+n 等。
// - mod 会被转换为 hotkeys-js 的 command（macOS）或 ctrl（其他平台）
// - 默认 preventDefault，避免浏览器默认行为
export interface UseShortcutOptions {
  enabled?: boolean
  preventDefault?: boolean
}

export function useShortcut(
  combo: string,
  handler: (e: KeyboardEvent) => void,
  { enabled = true, preventDefault = true }: UseShortcutOptions = {},
) {
  const handlerRef = useRef(handler)
  handlerRef.current = handler

  useEffect(() => {
    if (!enabled) return
    const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)
    const normalized = combo
      .split('+')
      .map((part) => part.trim().toLowerCase() === 'mod' ? (isMac ? 'command' : 'ctrl') : part.trim())
      .join('+')

    const wrapped = (event: KeyboardEvent) => {
      if (preventDefault) event.preventDefault()
      handlerRef.current(event)
    }

    hotkeys(normalized, wrapped)
    return () => {
      hotkeys.unbind(normalized, wrapped)
    }
  }, [combo, enabled, preventDefault])
}
