import { Button } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import IdeLayout from '../components/IdeLayout'
import ApiTreePanel from '../components/ApiTreePanel'
import ApiDebug from './ApiDebug'
import { useLayout } from './Layout'
import { PALETTE } from '../theme'

// 接口工作区：左侧目录树面板（ApiTreePanel）+ 右侧调试区（无选中时为新建/空状态）。
// 页面只负责路由/工作区协调；树的数据、拖拽、右键、目录 CRUD、导入导出全在 ApiTreePanel。
export default function Apis() {
  const { projectId, projects } = useLayout()
  const { id } = useParams() // /apis/:id 时右侧渲染调试区，左侧面板保持
  const nav = useNavigate()
  const [newMode, setNewMode] = useState(false)
  // 右键「新建接口」的目标目录（树节点 id）；undefined = 根
  const [createParentId, setCreateParentId] = useState<string>()
  // 保存后触发面板重载
  const [refresh, setRefresh] = useState(0)

  useEffect(() => {
    if (id) setNewMode(false)
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
    setCreateParentId(parentId)
    setNewMode(true)
    nav('/apis') // 清掉 :id，让工作区切到新建形态
  }

  const workspace = id ? (
    <ApiDebug key={id} onSaved={() => setRefresh((x) => x + 1)} />
  ) : newMode ? (
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
      <div style={{ color: PALETTE.textTertiary }}>从左侧选择接口，或直接输入 URL 调试</div>
      <Button type="primary" icon={<PlusOutlined />} onClick={() => openNewApi()}>新建接口</Button>
    </div>
  )

  return (
    <IdeLayout
      panel={
        <ApiTreePanel
          projectId={projectId}
          projects={projects}
          activeId={id}
          refresh={refresh}
          onPick={(a) => nav(`/apis/${a}`)}
          onNewApi={openNewApi}
        />
      }
    >
      {workspace}
    </IdeLayout>
  )
}
