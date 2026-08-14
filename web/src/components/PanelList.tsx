import type { ReactNode } from 'react'
import { Input, List, Space } from 'antd'
import { SearchOutlined } from '@ant-design/icons'
import { PALETTE } from '../theme'

// 二级面板通用列表：搜索 + 工具区 + 高亮选中行。
export default function PanelList<T extends { id: string }>({
  title, search, onSearch, extra, data, renderItem, activeId, onPick, empty,
}: {
  title: string
  search: string
  onSearch: (s: string) => void
  extra?: ReactNode
  data: T[]
  renderItem: (item: T) => ReactNode
  activeId?: string
  onPick?: (item: T) => void
  empty?: ReactNode
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: '10px 12px', borderBottom: `1px solid ${PALETTE.border}` }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
          <span style={{ fontSize: 15, fontWeight: 600, color: PALETTE.text }}>{title}</span>
          <Space size={6}>{extra}</Space>
        </div>
        <Input
          size="small" allowClear prefix={<SearchOutlined style={{ color: PALETTE.textTertiary }} />}
          placeholder="搜索…" value={search} onChange={(e) => onSearch(e.target.value)}
        />
      </div>
      <div style={{ flex: 1, overflow: 'auto' }}>
        <List
          size="small"
          dataSource={data}
          renderItem={(item) => (
            <div
              onClick={() => onPick?.(item)}
              style={{
                padding: '7px 12px', cursor: 'pointer', borderRadius: 6, margin: '2px 6px',
                background: item.id === activeId ? PALETTE.selectedRow : 'transparent',
              }}
            >
              {renderItem(item)}
            </div>
          )}
        />
        {data.length === 0 && (empty ?? (
          <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 32 }}>暂无数据</div>
        ))}
      </div>
    </div>
  )
}
