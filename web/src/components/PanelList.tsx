import type { ReactNode } from 'react'
import { Input, Space } from 'antd'
import { SearchOutlined } from '@ant-design/icons'
import { PALETTE } from '../theme'

// 二级面板通用列表：搜索 + 工具区 + 高亮选中行。
// （antd List 已废弃，这里直接 div 渲染——仅用到布局，无需 List 能力）
export default function PanelList<T extends { id: string }>({
  title, search, onSearch, extra, data, renderItem, activeId, onPick, empty, hideSearch,
}: {
  title: string
  search?: string
  onSearch?: (s: string) => void
  extra?: ReactNode
  data: T[]
  renderItem: (item: T) => ReactNode
  activeId?: string
  onPick?: (item: T) => void
  empty?: ReactNode
  hideSearch?: boolean
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: '10px 12px', borderBottom: `1px solid ${PALETTE.border}` }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: hideSearch ? 0 : 8 }}>
          <span style={{ fontSize: 15, fontWeight: 600, color: PALETTE.text }}>{title}</span>
          <Space size={6}>{extra}</Space>
        </div>
        {!hideSearch && (
          <Input
            size="small" allowClear prefix={<SearchOutlined style={{ color: PALETTE.textTertiary }} />}
            placeholder="搜索…" value={search ?? ''} onChange={(e) => onSearch?.(e.target.value)}
          />
        )}
      </div>
      <div style={{ flex: 1, overflow: 'auto' }}>
        {data.map((item) => (
          <div
            key={item.id}
            onClick={() => onPick?.(item)}
            style={{
              padding: '7px 12px', cursor: 'pointer', borderRadius: 6, margin: '2px 6px',
              background: item.id === activeId ? PALETTE.selectedRow : 'transparent',
            }}
          >
            {renderItem(item)}
          </div>
        ))}
        {data.length === 0 && (empty ?? (
          <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 32 }}>暂无数据</div>
        ))}
      </div>
    </div>
  )
}
