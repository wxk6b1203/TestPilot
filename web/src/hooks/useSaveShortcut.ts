import { useShortcut } from './useShortcut'
import { SHORTCUTS } from '../shortcuts'

// Cmd/Ctrl+S 触发保存（编辑器页通用）。具体按键定义集中在 src/shortcuts.ts。
export default function useSaveShortcut(onSave: () => void, enabled = true) {
  useShortcut(SHORTCUTS.save, onSave, { enabled })
}
