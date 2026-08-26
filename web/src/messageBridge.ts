// antd message 的 context 桥：静态 message.xxx 不消费 ConfigProvider 主题（antd v6
// 每次调用都告警），由 main.tsx 在 <App> 内注入 App.useApp() 实例后全局走实例。
// 注入前（极早期调用）退回静态方法；静态层同样开启堆叠，保证行为一致。
import { message as staticMessage } from 'antd'
import type { MessageInstance } from 'antd/es/message/interface'

staticMessage.config({ stack: true })

let instance: MessageInstance | null = null

export function bindMessageInstance(inst: MessageInstance) {
  instance = inst
}

export const message: MessageInstance = new Proxy({} as MessageInstance, {
  get(_target, key: string) {
    return (...args: unknown[]) => {
      const target = instance ?? staticMessage
      return (target as unknown as Record<string, (...a: unknown[]) => unknown>)[key](...args)
    }
  },
})
