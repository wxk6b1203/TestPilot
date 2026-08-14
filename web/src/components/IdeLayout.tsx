import type { ReactNode } from 'react'
import { PALETTE } from '../theme'

// IDE 三栏式页面外壳：左侧二级面板（固定宽、白底、右边界）+ 主工作区。
export default function IdeLayout({
  panel, panelWidth = 340, toolbar, children,
}: {
  panel?: ReactNode
  panelWidth?: number
  toolbar?: ReactNode
  children: ReactNode
}) {
  return (
    <div style={{ display: 'flex', height: '100%', background: PALETTE.bgLayout }}>
      {panel && (
        <div style={{
          flex: `0 0 ${panelWidth}px`, background: '#FFFFFF',
          borderRight: `1px solid ${PALETTE.border}`, overflow: 'hidden',
        }}>
          {panel}
        </div>
      )}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, background: '#FFFFFF' }}>
        {toolbar && (
          <div style={{ padding: '8px 16px', borderBottom: `1px solid ${PALETTE.border}`, background: '#FFFFFF' }}>
            {toolbar}
          </div>
        )}
        <div style={{ flex: 1, overflow: 'auto', minHeight: 0 }}>{children}</div>
      </div>
    </div>
  )
}
