import React, { useEffect } from 'react'
import ReactDOM from 'react-dom/client'
import { App as AntdApp, ConfigProvider } from 'antd'
import App from './App'
import { themeConfig } from './theme'
import { bindMessageInstance } from './messageBridge'
import { antdLocale, setBackendLang, useI18n } from './i18n'
import { fetchBackendLocale } from './api'
import './index.css'

// 启动时探测后端消息语言（恒为 en-US；失败不阻塞启动）
fetchBackendLocale().then(setBackendLang).catch(() => {})

// 在 <App> 内取 useApp 实例注入全局桥（messageBridge），静态调用全部转为 context 实例
function MessageBridge() {
  const { message } = AntdApp.useApp()
  useEffect(() => {
    bindMessageInstance(message)
  }, [message])
  return null
}

// ConfigProvider 的 locale 跟随 i18n 语言：useI18n 订阅切换并触发重渲染
function Root() {
  const { lang } = useI18n()
  return (
    <ConfigProvider locale={antdLocale(lang)} theme={themeConfig}>
      {/* antd App：让 message/Modal 走 context（消除 v6 静态调用警告）；stack 开启消息堆叠 */}
      <AntdApp message={{ stack: true }}>
        <MessageBridge />
        <App />
      </AntdApp>
    </ConfigProvider>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <Root />
  </React.StrictMode>,
)
