import { useEffect, useRef, useState } from 'react'
import { Modal } from 'antd'
import { useBlocker } from 'react-router-dom'

// 路由离开守卫：dirty 时拦截应用内跳转，确认后放行。
// - 首次导航（createHashRouter 的初始 POP，非 router 发起）不拦，否则浏览器告警
//   "blocker on a POP navigation to a location that was not created by router"；
// - 刷新/关闭窗口走原生 beforeunload 提示（blocker 管不到）。
// 返回 { guard: 需渲染的确认弹窗（null=未拦截）, allowOnce: 保存成功后跳转前调用，
// 让下一次导航放行一次（setSavedSnap 的提交晚于同步 nav，否则会被自己拦住） }。
export function useLeaveGuard(dirty: boolean, text = '当前内容尚未保存，离开将丢失修改。') {
  const [mounted, setMounted] = useState(false)
  useEffect(() => setMounted(true), [])
  const skipRef = useRef(false)
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (skipRef.current) {
      skipRef.current = false
      return false
    }
    return mounted && dirty && currentLocation.pathname !== nextLocation.pathname
  })
  // dirty 在拦截期间被保存消除时自动放行
  useEffect(() => {
    if (blocker.state === 'blocked' && !dirty) blocker.reset()
  }, [dirty, blocker])
  // 刷新/关闭：原生确认框
  useEffect(() => {
    if (!dirty) return
    const handler = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [dirty])
  const guard = blocker.state !== 'blocked' ? null : (
    <Modal
      open
      title="未保存的修改"
      okText="离开"
      okButtonProps={{ danger: true }}
      cancelText="留下"
      onOk={() => blocker.proceed()}
      onCancel={() => blocker.reset()}
    >
      {text}
    </Modal>
  )
  return { guard, allowOnce: () => { skipRef.current = true } }
}
