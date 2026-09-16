// Copilot 回收站：已删除会话列表（服务端分页）与彻底删除。
// 完全自治：挂载即拉第一页，分页与删除全部在内部完成；父级只负责进/出该视图。
import { useEffect, useState } from 'react'
import { Button, Pagination, Popconfirm, Typography } from 'antd'
import { ArrowLeftOutlined, DeleteOutlined } from '@ant-design/icons'
import { del, get } from '../../api'
import type { ListResp } from '../../api'
import { PALETTE } from '../../theme'
import { message } from '../../messageBridge'
import { t } from '../../i18n'

interface TrashSession {
  id: string
  title: string
  created_at: string
  deleted_at: string
  message_count: number
}

function formatTime(v: string) {
  if (!v) return ''
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return v
  return d.toLocaleString('zh-CN', { hour12: false })
}

export default function TrashView({ onBack }: { onBack: () => void }) {
  const [items, setItems] = useState<TrashSession[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)

  // 服务端分页（Pagination 翻页器）
  const load = (p = page, s = pageSize) =>
    get<ListResp<TrashSession>>(`/api/v1/copilot/trash?page=${p}&page_size=${s}`)
      .then((r) => {
        setItems(r.items ?? [])
        setTotal(r.total ?? 0)
      })
      .catch((e: any) => message.error(e.message))

  useEffect(() => {
    load(1)
  }, [])

  const purge = async (id: string) => {
    try {
      await del(`/api/v1/copilot/trash/${id}`)
      message.success(t('Permanently deleted'))
      // 当前页删空则回退一页
      const left = total - 1
      const maxPage = Math.max(1, Math.ceil(left / pageSize))
      const next = Math.min(page, maxPage)
      if (next !== page) setPage(next)
      await load(next)
    } catch (e: any) {
      message.error(e.message)
    }
  }

  return (
    <div style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', background: '#FFFFFF' }}>
      <div style={{
        padding: '8px 12px', borderBottom: `1px solid ${PALETTE.border}`,
        display: 'flex', alignItems: 'center', gap: 8, flexShrink: 0,
      }}>
        <Button size="small" icon={<ArrowLeftOutlined />} onClick={onBack}>{t('Back to chat')}</Button>
        <span style={{ fontWeight: 600, fontSize: 14, color: PALETTE.text }}>{t('Trash')}</span>
        <span style={{ fontSize: 12, color: PALETTE.textTertiary }}>{t('Auto-purged after 30 days')}</span>
      </div>
      <div style={{ flex: 1, minHeight: 0, overflowY: 'auto', padding: 12 }}>
        {items.length === 0 ? (
          <div style={{ textAlign: 'center', color: PALETTE.textTertiary, paddingTop: 80 }}>
            <DeleteOutlined style={{ fontSize: 28 }} />
            <div style={{ marginTop: 8 }}>{t('Trash is empty')}</div>
          </div>
        ) : (<>
          {items.map((item) => (
            <div
              key={item.id}
              style={{
                display: 'flex', alignItems: 'center', gap: 8,
                border: `1px solid ${PALETTE.border}`, borderRadius: 6,
                padding: '8px 10px', marginBottom: 8,
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <Typography.Text ellipsis style={{ fontSize: 13 }}>{item.title || t('New chat')}</Typography.Text>
                <div style={{ fontSize: 12, color: PALETTE.textTertiary, marginTop: 2 }}>
                  {t('{n} messages · deleted {time}', { n: item.message_count, time: formatTime(item.deleted_at) })}
                </div>
              </div>
              <Popconfirm
                title={t('Permanently deleted items cannot be recovered')}
                okText={t('Delete forever')}
                okButtonProps={{ danger: true }}
                onConfirm={() => void purge(item.id)}
              >
                <Button size="small" danger icon={<DeleteOutlined />}>{t('Delete forever')}</Button>
              </Popconfirm>
            </div>
          ))}
        </>
        )}
      </div>
      {total > 0 && (
        <div style={{ flexShrink: 0, padding: '8px 12px', borderTop: `1px solid ${PALETTE.border}`, display: 'flex', justifyContent: 'flex-end' }}>
          <Pagination
            size="small" current={page} pageSize={pageSize} total={total}
            showSizeChanger pageSizeOptions={[10, 20, 50]}
            showTotal={(total) => t('{total} items in total', { total })}
            onChange={(pg, ps) => {
              setPage(pg)
              setPageSize(ps)
              load(pg, ps)
            }}
          />
        </div>
      )}
    </div>
  )
}
