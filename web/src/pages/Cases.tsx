import { useEffect, useState } from 'react'
import { Button, Card, Popconfirm, Tag, message } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { del, get } from '../api'
import type { ListResp, TestCase } from '../api'
import IdeLayout from '../components/IdeLayout'
import PanelList from '../components/PanelList'
import { PALETTE, SPACING } from '../theme'
import { useLayout } from './Layout'

// 用例列表：左侧 PanelList（搜索/新建/删除），点击进入 CaseEditor。
export default function Cases() {
  const { projectId } = useLayout()
  const nav = useNavigate()
  const [rows, setRows] = useState<TestCase[]>([])
  const [search, setSearch] = useState('')
  const [activeId, setActiveId] = useState<string>()

  const load = () =>
    projectId
      ? get<ListResp<TestCase>>(`/api/v1/cases?project_id=${projectId}&page_size=200`).then((r) => setRows(r.items))
      : Promise.resolve()
  useEffect(() => {
    setRows([])
    load().catch((e) => message.error(e.message))
  }, [projectId])

  if (!projectId) return <Card>请先在顶部选择项目</Card>

  const kw = search.trim().toLowerCase()
  const data = kw
    ? rows.filter((c) => c.name.toLowerCase().includes(kw) || c.description.toLowerCase().includes(kw))
    : rows

  return (
    <IdeLayout
      panelWidth={360}
      panel={
        <PanelList
          title="用例"
          search={search}
          onSearch={setSearch}
          data={data}
          activeId={activeId}
          extra={
            <Button type="primary" size="small" icon={<PlusOutlined />} onClick={() => nav('/cases/new')}>
              + 新建
            </Button>
          }
          onPick={(c) => {
            setActiveId(c.id)
            nav(`/cases/${c.id}/edit`)
          }}
          renderItem={(c) => (
            <div style={{ display: 'flex', alignItems: 'center', gap: SPACING[2] }}>
              <span
                style={{
                  flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap', fontWeight: 500, fontSize: 13, color: PALETTE.text,
                }}
              >
                {c.name}
              </span>
              <Tag style={{ margin: 0, flexShrink: 0 }} color={c.type === 1 ? 'purple' : 'geekblue'}>
                {c.type === 1 ? '声明式' : '低代码'}
              </Tag>
              <Popconfirm
                title="删除该用例？"
                onConfirm={async () => {
                  try {
                    await del(`/api/v1/cases/${c.id}`)
                    message.success('已删除')
                    load()
                  } catch (e: any) {
                    message.error(e.message)
                  }
                }}
              >
                <Button
                  type="text" size="small" danger icon={<DeleteOutlined />}
                  style={{ flexShrink: 0 }}
                  onClick={(e) => e.stopPropagation()}
                />
              </Popconfirm>
            </div>
          )}
        />
      }
    >
      <div
        style={{
          height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexDirection: 'column', gap: SPACING[2],
        }}
      >
        <span style={{ fontSize: 14, color: PALETTE.textSecondary }}>
          从左侧选择用例，或点击「+ 新建」创建
        </span>
        <span style={{ fontSize: 12, color: PALETTE.textTertiary }}>
          声明式用例使用步骤树编辑器；低代码用例直接编写 Python
        </span>
      </div>
    </IdeLayout>
  )
}
