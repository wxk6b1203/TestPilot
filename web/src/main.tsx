import React, { useEffect } from 'react'
import ReactDOM from 'react-dom/client'
import { App as AntdApp, ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import App from './App'
import { themeConfig } from './theme'
import { bindMessageInstance } from './messageBridge'
import './index.css'

// 在 <App> 内取 useApp 实例注入全局桥（messageBridge），静态调用全部转为 context 实例
function MessageBridge() {
  const { message } = AntdApp.useApp()
  useEffect(() => {
    bindMessageInstance(message)
  }, [message])
  return null
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider locale={zhCN} theme={themeConfig}>
      {/* antd App：让 message/Modal 走 context（消除 v6 静态调用警告） */}
      <AntdApp>
        <MessageBridge />
        <App />
      </AntdApp>
    </ConfigProvider>
  </React.StrictMode>,
)
