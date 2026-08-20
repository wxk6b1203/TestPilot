import { useEffect, useRef } from 'react'
import { getToken } from '../api'

// SSE 订阅 hook：用 fetch + ReadableStream 解析 text/event-stream。
// 不使用原生 EventSource，因为需要携带 Authorization header（JWT/API token）。
// 断线后 3s 自动重连；组件卸载或 channels 变化时 abort。
export function useEventStream(
  channels: string[],
  onEvent: (event: string, data: any) => void,
  enabled = true,
) {
  const channelsKey = channels.filter(Boolean).join(',')
  const handlerRef = useRef(onEvent)
  handlerRef.current = onEvent

  useEffect(() => {
    if (!enabled || !channelsKey) return
    const ctrl = new AbortController()
    let stopped = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const connect = async () => {
      if (stopped) return
      try {
        const res = await fetch(
          `/api/v1/events?channels=${encodeURIComponent(channelsKey)}`,
          {
            headers: { Authorization: `Bearer ${getToken()}` },
            signal: ctrl.signal,
          },
        )
        if (!res.ok || !res.body) throw new Error(`SSE HTTP ${res.status}`)
        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          let idx: number
          while ((idx = buffer.indexOf('\n\n')) >= 0) {
            const frame = buffer.slice(0, idx)
            buffer = buffer.slice(idx + 2)
            let event = 'message'
            const dataLines: string[] = []
            for (const line of frame.split('\n')) {
              if (line.startsWith(':')) continue
              if (line.startsWith('event:')) event = line.slice(6).trim()
              else if (line.startsWith('data:')) dataLines.push(line.slice(5).trimStart())
            }
            if (!dataLines.length) continue
            const text = dataLines.join('\n')
            try {
              handlerRef.current(event, text ? JSON.parse(text) : null)
            } catch {
              /* 非 JSON 事件（未来扩展）忽略 */
            }
          }
        }
      } catch (e: any) {
        if (e?.name === 'AbortError') return
        // 连接失败/断开：3s 后重连
      }
      if (!stopped) timer = setTimeout(() => void connect(), 3000)
    }

    void connect()
    return () => {
      stopped = true
      if (timer) clearTimeout(timer)
      ctrl.abort()
    }
  }, [channelsKey, enabled])
}
