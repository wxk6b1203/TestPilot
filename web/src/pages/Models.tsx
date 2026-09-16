import { Button } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import IdeLayout from '../components/IdeLayout'
import ModelTreePanel from '../components/ModelTreePanel'
import ModelDetail from './ModelDetail'
import { useLayout } from '../hooks/useLayout'
import { PALETTE } from '../theme'

// 结构（数据模型）工作区：独立一级页签——左侧模型目录树，右侧模型编辑器。
// 与接口页同构：页面只负责路由/工作区协调，树数据与目录管理都在 ModelTreePanel。
export default function Models() {
  const { projectId, projects } = useLayout()
  const { id } = useParams() // /models/:id 右侧渲染模型编辑器
  const location = useLocation()
  const nav = useNavigate()
  // 保存后触发面板重载
  const [refresh, setRefresh] = useState(0)
  // 项目切换 / 当前模型被删除时锁定右侧工作区
  const [workspaceNotice, setWorkspaceNotice] = useState<string>()

  // 新建模式走路由 state：被未保存离开守卫拦截时不会留下“newMode 已置位但路由没变”的脏状态
  const locationState = location.state as { newModel?: boolean; modelParentId?: string } | null
  const newMode = !id && locationState?.newModel === true
  const createParentId = locationState?.modelParentId

  if (!projectId)
    return (
      <div style={{
        height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: PALETTE.bgLayout, color: PALETTE.textTertiary,
      }}>
        请先选择项目
      </div>
    )

  const openNewModel = (parentId?: string) => {
    setWorkspaceNotice(undefined)
    nav('/models', { state: { newModel: true, modelParentId: parentId } })
  }

  const workspace = !workspaceNotice && (id || newMode) ? (
    id ? (
      <ModelDetail key={id} onSaved={() => setRefresh((x) => x + 1)} />
    ) : (
      <ModelDetail
        key={`new-${createParentId ?? 'root'}`} // 换目标目录时重置表单
        newMode
        createParentId={createParentId}
        onSaved={() => setRefresh((x) => x + 1)}
      />
    )
  ) : (
    <div style={{
      height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center',
      justifyContent: 'center', gap: 12, background: '#FFFFFF',
    }}>
      <div style={{ color: PALETTE.textTertiary }}>
        {workspaceNotice ?? '从左侧选择数据模型，或新建一个结构'}
      </div>
      <Button type="primary" icon={<PlusOutlined />} onClick={() => openNewModel()}>新建结构</Button>
    </div>
  )

  return (
    <IdeLayout
      panel={
        <ModelTreePanel
          projectId={projectId}
          projects={projects}
          activeId={id}
          refresh={refresh}
          onPick={(m) => {
            setWorkspaceNotice(undefined)
            nav(`/models/${m}`)
          }}
          onNewModel={openNewModel}
          onDeleted={(deletedId) => {
            if (deletedId === id) {
              setWorkspaceNotice('当前结构已删除，请重新选择')
              nav('/models', { replace: true, state: null })
            }
          }}
        />
      }
    >
      {workspace}
    </IdeLayout>
  )
}
