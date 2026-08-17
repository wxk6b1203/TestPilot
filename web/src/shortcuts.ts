// 全局快捷键集中注册表。
// 后续新增快捷键时在这里定义组合键，并在页面/组件中通过 useShortcut 消费。
export const SHORTCUTS = {
  save: 'mod+s',
  send: 'mod+enter',
} as const

export type ShortcutName = keyof typeof SHORTCUTS
