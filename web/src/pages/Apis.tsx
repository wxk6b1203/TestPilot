import { Button } from 'antd'
import { CodeOutlined, PlusOutlined } from '@ant-design/icons'
import { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import IdeLayout from '../components/IdeLayout'
import { get } from '../api'
import WrapperPreviewModal from '../components/WrapperPreviewModal'
import { message } from '../messageBridge'
import ApiTreePanel from '../components/ApiTreePanel'
import ApiDebug from './ApiDebug'
import { useLayout } from '../hooks/useLayout'
import { PALETTE } from '../theme'

// 接口工作区：左侧目录树面板（ApiTreePanel）+ 右侧调试区（无选中时为新建/空状态）。
// 页面只负责路由/工作区协调；树的数据、拖拽、右键、目录 CRUD、导入导出全在 ApiTreePanel。
export default function Apis() {
  const { projectId, projects } = useLayout()
  const { id } = useParams() // /apis/:id 时右侧渲染调试区，左侧面板保持
  const location = useLocation()
  const nav = useNavigate()
  // 保存后触发面板重载
  const [refresh, setRefresh] = useState(0)
  // 项目切换 / 当前接口被删除时锁定右侧工作区，显示提示而不是继续编辑失效数据
  const [workspaceNotice, setWorkspaceNotice] = useState<string>()
  const [wrappers, setWrappers] = useState('')
  const [wrappersLoading, setWrappersLoading] = useState(false)
  const prevProjectRef = useRef(projectId)

  const previewWrappers = async () => {
    if (!projectId) {
      message.warning('请先选择项目')
      return
    }
    setWrappersLoading(true)
    try {
      const r = await get<{ source: string }>(`/api/v1/projects/${projectId}/api-wrappers`)
      setWrappers(r.source || '# （项目内暂无接口）')
    } catch (e: any) {
      message.error(e.message)
    } finally {
      setWrappersLoading(false)
    }
  }

  // 新建模式放进路由 state：导航被未保存离开守卫拦截时，不会留下“newMode 已置位但路由没变”的脏状态
  const locationState = location.state as { newApi?: boolean; parentId?: string } | null
  const newMode = !id && locationState?.newApi === true
  const createParentId = locationState?.parentId

  // 项目变化：清掉当前接口/新建状态，避免右侧继续用旧项目 id 编辑（保存会把 project_id 写串）
  useEffect(() => {
    if (prevProjectRef.current === projectId) return
    prevProjectRef.current = projectId
    setWorkspaceNotice('项目已切换，请从左侧重新选择接口')
    nav('/apis', { replace: true, state: null })
  }, [projectId, nav])

  // 选中有效接口后清除提示
  useEffect(() => {
    if (id) setWorkspaceNotice(undefined)
  }, [id])

  if (!projectId)
    return (
      <div style={{
        height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
        background: PALETTE.bgLayout, color: PALETTE.textTertiary,
      }}>
        请先选择项目
      </div>
    )

  const openNewApi = (parentId?: string) => {
    setWorkspaceNotice(undefined)
    nav('/apis', { state: { newApi: true, parentId } })
  }

  const workspace = !workspaceNotice && id ? (
    <ApiDebug key={id} onSaved={() => setRefresh((x) => x + 1)} />
  ) : !workspaceNotice && newMode ? (
    <ApiDebug
      key={`new-${createParentId ?? 'root'}`} // 换目标目录时重置表单
      newMode
      createParentId={createParentId}
      onSaved={() => setRefresh((x) => x + 1)}
    />
  ) : (
    <div style={{
      height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center',
      justifyContent: 'center', gap: 12, background: '#FFFFFF',
    }}>
      <div style={{ color: PALETTE.textTertiary }}>
        {workspaceNotice ?? '从左侧选择接口，或直接输入 URL 调试'}
      </div>
      <Button type="primary" icon={<PlusOutlined />} onClick={() => openNewApi()}>新建接口</Button>
      <Button size="small" icon={<CodeOutlined />} loading={wrappersLoading} onClick={previewWrappers}>
        查看接口封装
      </Button>
    </div>
  )

  return (
    <>
    <IdeLayout
      panel={
        <ApiTreePanel
          projectId={projectId}
          projects={projects}
          activeId={id}
          refresh={refresh}
          onPick={(a) => {
            setWorkspaceNotice(undefined)
            nav(`/apis/${a}`)
          }}
          onNewApi={openNewApi}
          onDeleted={(deletedId) => {
            if (deletedId === id) {
              setWorkspaceNotice('当前接口已删除，请重新选择接口')
              nav('/apis', { replace: true, state: null })
            }
          }}
        />
      }
    >
      {workspace}
    </IdeLayout>
    <WrapperPreviewModal
      open={!!wrappers}
      source={wrappers}
      baseUrl={`/api/v1/projects/${projectId}/api-wrappers`}
      title="tp_api_wrappers.py（项目全部接口）"
      onClose={() => setWrappers('')}
    />
    </>
  )
}
