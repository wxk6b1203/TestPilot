// LLM 文本的 Markdown 渲染：marked 解析 + DOMPurify 白名单清洗（防 XSS）
import { useMemo } from 'react'
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import './MarkdownView.css'

const ALLOWED_TAGS = [
  'a', 'p', 'br', 'hr',
  'strong', 'em', 'del', 'code', 'pre', 'blockquote',
  'ul', 'ol', 'li',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'table', 'thead', 'tbody', 'tr', 'th', 'td',
]
const ALLOWED_ATTR = ['href', 'title', 'class']

export default function MarkdownView({ text }: { text: string }) {
  const html = useMemo(() => {
    if (!text) return ''
    const raw = marked.parse(text, { async: false, gfm: true, breaks: true }) as string
    return DOMPurify.sanitize(raw, { ALLOWED_TAGS, ALLOWED_ATTR })
  }, [text])
  // 内容已经 DOMPurify 白名单清洗；链接仅保留 href/title，无事件与脚本注入面
  return <div className="tp-markdown" dangerouslySetInnerHTML={{ __html: html }} />
}
