import type { ReactNode } from 'react'
import { PALETTE } from '../theme'
import SplitPane from './SplitPane'

// IDE 三栏式页面外壳：左侧二级面板（可拖拽调宽）+ 主工作区。
export default function IdeLayout({
  panel, panelWidth = 340, toolbar, children,
}: {
  panel?: ReactNode
  panelWidth?: number
  toolbar?: ReactNode
  children: ReactNode
}) {
  const workspace = (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column', minWidth: 0, background: '#FFFFFF' }}>
      {toolbar && (
        <div style={{ padding: '8px 16px', borderBottom: `1px solid ${PALETTE.border}`, background: '#FFFFFF' }}>
          {toolbar}
        </div>
      )}
      <div style={{ flex: 1, overflow: 'auto', minHeight: 0 }}>{children}</div>
    </div>
  )
  if (!panel) return workspace
  return (
    <SplitPane direction="horizontal" initial={panelWidth} min={200} max={700}>
      <div style={{ height: '100%', background: '#FFFFFF' }}>{panel}</div>
      {workspace}
    </SplitPane>
  )
}
