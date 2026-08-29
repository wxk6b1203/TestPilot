// Copilot 会话侧栏：新会话入口、会话卡片（服务端分页 50/页）与回收站入口。
// 列表数据自持；打开/删除/新建等动作通过回调交回父级（对话状态归父级）。
import { useEffect, useState } from 'react'
import { Button, Pagination, Popconfirm, Space, Typography } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { del, get } from '../../api'
import type { ListResp } from '../../api'
import { PALETTE } from '../../theme'
import { message } from '../../messageBridge'

const PAGE_SIZE = 50

interface Session {
  id: string
  title: string
  created_at: string
}

interface SessionListProps {
  /** 当前打开的会话 ID（高亮卡片） */
  activeId: string
  /** 回收站视图激活时入口按钮呈主色 */
  trashActive: boolean
  /** 父级递增该值时回到第一页重拉（新建会话置顶可见） */
  refreshKey: number
  onOpen: (sid: string) => void
  /** 删除成功后回调（父级判断是否收起当前打开的会话） */
  onDeleted: (sid: string) => void
  onNew: () => void
  onOpenTrash: () => void
}

export default function SessionList({
  activeId, trashActive, refreshKey, onOpen, onDeleted, onNew, onOpenTrash,
}: SessionListProps) {
  const [sessions, setSessions] = useState<Session[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)

  const load = (p: number) =>
    get<ListResp<Session>>(`/api/v1/copilot/sessions?page=${p}&page_size=${PAGE_SIZE}`)
      .then((r) => {
        // page 随数据一起更新（在异步回调里写，避免 effect 同步 setState）
        setPage(p)
        setSessions(r.items ?? [])
        setTotal(r.total ?? 0)
      })
      .catch((e: any) => message.error(e.message))

  // 挂载与 refreshKey 变化（父级新建会话）时回到第一页重拉
  useEffect(() => {
    load(1)
  }, [refreshKey])

  const confirmDelete = async (s: Session) => {
    try {
      await del(`/api/v1/copilot/sessions/${s.id}`)
      message.success('已移入回收站')
      // 当前页删空则回退一页（与回收站翻页器同策略）
      const left = total - 1
      const maxPage = Math.max(1, Math.ceil(left / PAGE_SIZE))
      const next = Math.min(page, maxPage)
      await load(next)
      onDeleted(s.id)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  return (
    <div style={{
      flex: '0 0 260px', minWidth: 0, borderRight: `1px solid ${PALETTE.border}`,
      display: 'flex', flexDirection: 'column', padding: 8, background: '#FFFFFF',
    }}>
      <Button block icon={<PlusOutlined />} onClick={onNew} style={{ marginBottom: 8, flexShrink: 0 }}>
        新会话
      </Button>
      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto' }}>
        {sessions.map((s) => (
          <div
            key={s.id}
            onClick={() => onOpen(s.id)}
            style={{
              cursor: 'pointer',
              display: 'flex', alignItems: 'center', gap: 8,
              border: `1px solid ${PALETTE.border}`, borderRadius: 6,
              padding: '8px 10px', marginBottom: 8,
              background: s.id === activeId ? PALETTE.selectedRow : undefined,
            }}
          >
            <Typography.Text ellipsis style={{ fontSize: 13, flex: 1, minWidth: 0 }}>{s.title || '新对话'}</Typography.Text>
            <Space size={4} onClick={(e) => e.stopPropagation()}>
              <Popconfirm
                title="删除会话？"
                description="删除后会移入回收站，30 天后自动清理"
                onConfirm={async () => {
                  await confirmDelete(s)
                }}
              >
                <Button size="small" danger type="text" icon={<DeleteOutlined />} />
              </Popconfirm>
            </Space>
          </div>
        ))}
        {sessions.length === 0 && (
          <div style={{ textAlign: 'center', color: PALETTE.textTertiary, padding: 24, fontSize: 12 }}>暂无会话</div>
        )}
      </div>
      {total > 0 && (
        <div style={{ flexShrink: 0, display: 'flex', justifyContent: 'center', paddingTop: 6 }}>
          <Pagination
            size="small" current={page} pageSize={PAGE_SIZE} total={total}
            hideOnSinglePage
            onChange={(pg) => load(pg)}
          />
        </div>
      )}
      {/* 固定底栏：回收站入口（负外边距抵消侧栏 padding，分隔线贯穿左右） */}
      <div style={{ flexShrink: 0, margin: '8px -8px 0', padding: '8px 8px 0', borderTop: `1px solid ${PALETTE.border}` }}>
        <Button
          size="small" block icon={<DeleteOutlined />}
          type={trashActive ? 'primary' : 'default'}
          onClick={onOpenTrash}
        >
          回收站
        </Button>
      </div>
    </div>
  )
}
