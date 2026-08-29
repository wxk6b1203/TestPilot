// Copilot 会话侧栏：新会话入口、会话卡片（服务端分页 50/页）与回收站入口。
// 列表数据自持；打开/删除/新建等动作通过回调交回父级（对话状态归父级）。
// 会话卡片支持右键菜单与三点按钮（同一组动作：重命名 / 删除），重命名原地编辑
// （标题变输入框，Enter 提交、Esc 取消、失焦提交）。
import { useEffect, useRef, useState } from 'react'
import { App as AntdApp, Button, Dropdown, Input, Pagination, Typography } from 'antd'
import type { MenuProps } from 'antd'
import { DeleteOutlined, EditOutlined, MoreOutlined, PlusOutlined } from '@ant-design/icons'
import { del, get, put } from '../../api'
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
  const { modal } = AntdApp.useApp()
  const [sessions, setSessions] = useState<Session[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  // 原地重命名：editingId 标记哪张卡片在编辑；committingRef 防止 Enter 提交后
  // 输入框卸载/失焦再次触发 commit 造成重复请求
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editingTitle, setEditingTitle] = useState('')
  const committingRef = useRef(false)

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

  const doDelete = async (s: Session) => {
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

  const confirmDelete = (s: Session) => {
    modal.confirm({
      title: `删除会话「${s.title || '新对话'}」？`,
      content: '删除后会移入回收站，30 天后自动清理',
      okText: '删除',
      okButtonProps: { danger: true },
      cancelText: '取消',
      onOk: () => doDelete(s),
    })
  }

  // ---- 原地重命名 ----
  const startRename = (s: Session) => {
    committingRef.current = false
    setEditingId(s.id)
    setEditingTitle(s.title)
  }

  const cancelRename = () => {
    committingRef.current = false
    setEditingId(null)
    setEditingTitle('')
  }

  const commitRename = async () => {
    if (committingRef.current) return
    const sid = editingId
    const current = sessions.find((x) => x.id === sid)
    if (!sid || !current) {
      cancelRename()
      return
    }
    const title = editingTitle.trim()
    if (!title) {
      message.warning('标题不能为空')
      return
    }
    if (title === current.title) {
      cancelRename()
      return
    }
    committingRef.current = true
    try {
      await put(`/api/v1/copilot/sessions/${sid}`, { title })
      setSessions((prev) => prev.map((x) => (x.id === sid ? { ...x, title } : x)))
      message.success('已重命名')
      cancelRename()
    } catch (e: any) {
      message.error(e.message)
    } finally {
      committingRef.current = false
    }
  }

  // 卡片菜单：三点按钮（click 触发）与整卡右键（contextMenu 触发）共用
  const sessionMenu = (s: Session): MenuProps => ({
    items: [
      { key: 'rename', label: '重命名', icon: <EditOutlined /> },
      { type: 'divider' },
      { key: 'del', label: '删除', icon: <DeleteOutlined />, danger: true },
    ],
    onClick: ({ key }) => {
      if (key === 'rename') startRename(s)
      else if (key === 'del') confirmDelete(s)
    },
  })

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
          <Dropdown key={s.id} trigger={['contextMenu']} menu={sessionMenu(s)}>
            <div
              onClick={() => { if (editingId !== s.id) onOpen(s.id) }}
              style={{
                cursor: 'pointer',
                display: 'flex', alignItems: 'center', gap: 8,
                border: `1px solid ${PALETTE.border}`, borderRadius: 6,
                padding: '8px 10px', marginBottom: 8,
                background: s.id === activeId ? PALETTE.selectedRow : undefined,
              }}
            >
              {editingId === s.id ? (
                <Input
                  size="small"
                  autoFocus
                  value={editingTitle}
                  onChange={(e) => setEditingTitle(e.target.value)}
                  onClick={(e) => e.stopPropagation()}
                  onPressEnter={commitRename}
                  onBlur={commitRename}
                  onKeyDown={(e) => { if (e.key === 'Escape') cancelRename() }}
                  onFocus={(e) => e.currentTarget.select()}
                  style={{ flex: 1, minWidth: 0, fontSize: 13 }}
                />
              ) : (
                <>
                  <Typography.Text ellipsis style={{ fontSize: 13, flex: 1, minWidth: 0 }}>
                    {s.title || '新对话'}
                  </Typography.Text>
                  <Dropdown trigger={['click']} menu={sessionMenu(s)} placement="bottomRight">
                    <Button
                      size="small" type="text" icon={<MoreOutlined />}
                      title="更多操作"
                      style={{ color: PALETTE.textTertiary, flexShrink: 0 }}
                      onClick={(e) => e.stopPropagation()}
                    />
                  </Dropdown>
                </>
              )}
            </div>
          </Dropdown>
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
