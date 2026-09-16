// 轻量 i18n：以英文文案为 key，zh.ts 提供「英文 → 中文」映射，缺失时回退英文原文。
// i18n 策略：后端（scheduler/worker/copilot 工具面）消息恒为英文（GET /api/v1/meta/locale
// 可查询），所有本地化展示都在前端完成。
// 语言持久化在 localStorage('tp_lang')；模块级状态 + useSyncExternalStore 触发全树重渲染，
// 非 React 环境（api.ts 等）直接 import { t } 使用当前语言。
import { useSyncExternalStore } from 'react'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import zh from './zh'

export type Lang = 'zh' | 'en'

export const LANG_KEY = 'tp_lang'

export const LANGS: { value: Lang; label: string }[] = [
  { value: 'zh', label: '中文' },
  { value: 'en', label: 'English' },
]

function initLang(): Lang {
  try {
    const v = localStorage.getItem(LANG_KEY)
    if (v === 'en' || v === 'zh') return v
  } catch { /* 隐私模式等场景下 localStorage 不可用：忽略走默认 */ }
  return 'zh'
}

let current: Lang = initLang()
const subs = new Set<() => void>()

export function getLang(): Lang {
  return current
}

export function setLang(lang: Lang): void {
  if (lang === current) return
  current = lang
  try { localStorage.setItem(LANG_KEY, lang) } catch { /* 同上：忽略存储失败 */ }
  document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en'
  subs.forEach((fn) => fn())
}
document.documentElement.lang = current === 'zh' ? 'zh-CN' : 'en'

// 占位符用单花括号 {name}；文案里的 {{var}}（变量模板示例）不会被误替换。
// 英文是源文案（key 本身），中文模式下查词典、英文模式直接用 key。
export function t(key: string, params?: Record<string, string | number>): string {
  let s = current === 'zh' ? (zh[key] ?? key) : key
  if (params) {
    s = s.replace(/\{(\w+)\}/g, (m, k: string) =>
      (params as Record<string, string | number>)[k] !== undefined ? String(params[k]) : m)
  }
  return s
}

function subscribe(cb: () => void): () => void {
  subs.add(cb)
  return () => { subs.delete(cb) }
}

// React 组件入口：订阅语言变化触发重渲染；纯函数场景直接用模块级 t()
export function useI18n(): { lang: Lang; setLang: (l: Lang) => void; t: typeof t } {
  useSyncExternalStore(subscribe, getLang)
  return { lang: current, setLang, t }
}

// antd 内建文案（分页/日期选择/空态等）跟随语言
export function antdLocale(lang: Lang) {
  return lang === 'zh' ? zhCN : enUS
}

// 后端消息语言（来自 GET /api/v1/meta/locale 启动探测，恒为 en-US）。
// 后端消息不做运行时翻译、原样展示；记录在此供需要按后端语言分支的场景读取。
let backendLang = 'en-US'
export function getBackendLang(): string {
  return backendLang
}
export function setBackendLang(lang: string): void {
  backendLang = lang
}
