import { useState } from 'react'
import { Card } from 'antd'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { post } from '../api'
import IdeLayout from '../components/IdeLayout'
import EntityTreePanel from '../components/EntityTreePanel'
import { useLayout } from '../hooks/useLayout'
import CaseEditor from './CaseEditor'
import { t } from '../i18n'

// 用例列表：左侧目录树（EntityTreePanel），右侧为编辑器（/cases/:id/edit 或 /cases/new）。
export default function Cases() {
  const { projectId } = useLayout()
  const { id } = useParams()
  const loc = useLocation()
  const nav = useNavigate()
  const [refresh, setRefresh] = useState(0)
  const locationState = loc.state as { parentId?: string } | null
  const createParentId = locationState?.parentId

  if (!projectId) return <Card>{t('Select a project at the top first')}</Card>

  const openNewCase = (parentId?: string) => {
    nav('/cases/new', { state: parentId ? { parentId } : undefined })
  }

  const handleSaved = (newId?: string) => {
    // 从目录右键「新建用例」创建成功后，把新用例挂到目标目录
    if (newId && createParentId) {
      post('/api/v1/tree/nodes', {
        project_id: projectId,
        ref_type: 4,
        ref_id: newId,
        parent_id: createParentId,
      }).catch(() => {})
    }
    setRefresh((x) => x + 1)
  }

  return (
    <IdeLayout
      panelWidth={360}
      panel={
        <EntityTreePanel
          title={t('Cases')}
          kind="case"
          projectId={projectId}
          activeId={id}
          refresh={refresh}
          onPick={(cid) => nav(`/cases/${cid}/edit`)}
          onNewInFolder={openNewCase}
          onDeleted={(deletedId) => {
            if (deletedId === id) nav('/cases', { replace: true, state: null })
          }}
        />
      }
    >
      {id || loc.pathname === '/cases/new' ? (
        <CaseEditor key={id ?? 'new'} onSaved={handleSaved} />
      ) : (
        <div
          style={{
            height: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center',
            flexDirection: 'column', gap: 12,
          }}
        >
          <span style={{ fontSize: 14, color: '#646A73' }}>
            {t('Pick a case on the left, or click "+ New" to create one')}
          </span>
          <span style={{ fontSize: 12, color: '#BBBFC4' }}>
            {t('Declarative cases use the step-tree editor; low-code cases are plain Python')}
          </span>
        </div>
      )}
    </IdeLayout>
  )
}
